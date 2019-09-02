package cbft

import (
	"container/list"
	"fmt"
	"sort"
	"time"

	"github.com/PlatONnetwork/PlatON-Go/consensus/cbft/state"

	"github.com/PlatONnetwork/PlatON-Go/common"
	"github.com/PlatONnetwork/PlatON-Go/consensus/cbft/network"
	"github.com/PlatONnetwork/PlatON-Go/consensus/cbft/protocols"
	ctypes "github.com/PlatONnetwork/PlatON-Go/consensus/cbft/types"
	"github.com/PlatONnetwork/PlatON-Go/consensus/cbft/utils"
	"github.com/PlatONnetwork/PlatON-Go/core/types"
)

// Get the block from the specified connection, get the block into the fetcher, and execute the block CBFT update state machine
func (cbft *Cbft) fetchBlock(id string, hash common.Hash, number uint64) {
	if cbft.fetcher.Len() != 0 {
		cbft.log.Trace("Had fetching block")
		return
	}

	baseBlockHash, baseBlockNumber := common.Hash{}, uint64(0)
	var parentBlock *types.Block

	if cbft.state.HighestQCBlock().NumberU64() < number {
		parentBlock = cbft.state.HighestQCBlock()
		baseBlockHash, baseBlockNumber = parentBlock.Hash(), parentBlock.NumberU64()
	} else if cbft.state.HighestQCBlock().NumberU64() == number {
		parentBlock = cbft.state.HighestLockBlock()
		baseBlockHash, baseBlockNumber = parentBlock.Hash(), parentBlock.NumberU64()
	} else {
		cbft.log.Trace("No suitable block need to request")
		return
	}

	match := func(msg ctypes.Message) bool {
		_, ok := msg.(*protocols.QCBlockList)
		return ok
	}

	executor := func(msg ctypes.Message) {
		defer func() {
			cbft.log.Debug("Close fetching")
			utils.SetFalse(&cbft.fetching)
		}()
		if blockList, ok := msg.(*protocols.QCBlockList); ok {
			// Execution block
			for i, block := range blockList.Blocks {
				if block.ParentHash() != parentBlock.Hash() {
					cbft.log.Debug("Response block's is error",
						"blockHash", block.Hash(), "blockNumber", block.NumberU64(),
						"parentHash", parentBlock.Hash(), "parentNumber", parentBlock.NumberU64())
					return
				}
				if err := cbft.verifyPrepareQC(block.NumberU64(), block.Hash(), blockList.QC[i]); err != nil {
					cbft.log.Error("Verify block prepare qc failed", "hash", block.Hash(), "number", block.NumberU64(), "error", err)
					return
				}
				start := time.Now()
				if err := cbft.blockCacheWriter.Execute(block, parentBlock); err != nil {
					cbft.log.Error("Execute block failed", "hash", block.Hash(), "number", block.NumberU64(), "error", err)
					return
				}
				blockExecutedTimer.UpdateSince(start)
				parentBlock = block
			}

			// Update the results to the CBFT state machine
			cbft.asyncCallCh <- func() {
				if err := cbft.OnInsertQCBlock(blockList.Blocks, blockList.QC); err != nil {
					cbft.log.Error("Insert block failed", "error", err)
				}
			}
		}
	}

	expire := func() {
		cbft.log.Debug("Fetch timeout, close fetching", "targetId", id, "baseBlockHash", baseBlockHash, "baseBlockNumber", baseBlockNumber)
		utils.SetFalse(&cbft.fetching)
	}

	cbft.log.Debug("Start fetching")

	utils.SetTrue(&cbft.fetching)
	cbft.fetcher.AddTask(id, match, executor, expire)
	cbft.network.Send(id, &protocols.GetQCBlockList{BlockHash: baseBlockHash, BlockNumber: baseBlockNumber})
}

// Obtain blocks that are not in the local according to the proposed block
func (cbft *Cbft) prepareBlockFetchRules(id string, pb *protocols.PrepareBlock) {
	if pb.Block.NumberU64() > cbft.state.HighestQCBlock().NumberU64() {
		for i := uint32(0); i < pb.BlockIndex; i++ {
			b, _ := cbft.state.ViewBlockAndQC(i)
			if b == nil {
				cbft.SyncPrepareBlock(id, cbft.state.Epoch(), cbft.state.ViewNumber(), i)
			}
		}
	}
}

// Get votes and blocks that are not available locally based on the height of the vote
func (cbft *Cbft) prepareVoteFetchRules(id string, vote *protocols.PrepareVote) {
	// Greater than QC+1 means the vote is behind
	if vote.BlockNumber > cbft.state.HighestQCBlock().NumberU64()+1 {
		for i := uint32(0); i < vote.BlockIndex; i++ {
			b, q := cbft.state.ViewBlockAndQC(i)
			if b == nil {
				cbft.SyncPrepareBlock(id, cbft.state.Epoch(), cbft.state.ViewNumber(), i)
			} else if q == nil {
				cbft.SyncBlockQuorumCert(id, b.NumberU64(), b.Hash())
			}
		}
	}
}

// OnGetPrepareBlock handles the  message type of GetPrepareBlockMsg.
func (cbft *Cbft) OnGetPrepareBlock(id string, msg *protocols.GetPrepareBlock) error {
	if msg.Epoch == cbft.state.Epoch() && msg.ViewNumber == cbft.state.ViewNumber() {
		prepareBlock := cbft.state.PrepareBlockByIndex(msg.BlockIndex)
		if prepareBlock != nil {
			cbft.log.Debug("Send PrepareBlock", "prepareBlock", prepareBlock.String())
			cbft.network.Send(id, prepareBlock)
		}
	}
	return nil
}

// OnGetBlockQuorumCert handles the message type of GetBlockQuorumCertMsg.
func (cbft *Cbft) OnGetBlockQuorumCert(id string, msg *protocols.GetBlockQuorumCert) error {
	_, qc := cbft.blockTree.FindBlockAndQC(msg.BlockHash, msg.BlockNumber)
	if qc != nil {
		cbft.network.Send(id, &protocols.BlockQuorumCert{BlockQC: qc})
	}
	return nil
}

// OnBlockQuorumCert handles the message type of BlockQuorumCertMsg.
func (cbft *Cbft) OnBlockQuorumCert(id string, msg *protocols.BlockQuorumCert) error {
	if msg.BlockQC.Epoch != cbft.state.Epoch() || msg.BlockQC.ViewNumber != cbft.state.ViewNumber() {
		cbft.log.Debug("Receive BlockQuorumCert response failed", "local.epoch", cbft.state.Epoch(), "local.viewNumber", cbft.state.ViewNumber(), "msg", msg.String())
		return fmt.Errorf("msg is not match current state")
	}

	if _, qc := cbft.blockTree.FindBlockAndQC(msg.BlockQC.BlockHash, msg.BlockQC.BlockNumber); qc != nil {
		cbft.log.Debug("Block has exist", "msg", msg.String())
		return fmt.Errorf("block already exists")
	}

	// If blockQC comes the block must exist
	block := cbft.state.ViewBlockByIndex(msg.BlockQC.BlockIndex)
	if block == nil {
		cbft.log.Debug("Block not exist", "msg", msg.String())
		return fmt.Errorf("block not exist")
	}
	if err := cbft.verifyPrepareQC(block.NumberU64(), block.Hash(), msg.BlockQC); err != nil {
		return &authFailedError{err}
	}

	cbft.insertPrepareQC(msg.BlockQC)
	return nil
}

// OnGetQCBlockList handles the message type of GetQCBlockListMsg.
func (cbft *Cbft) OnGetQCBlockList(id string, msg *protocols.GetQCBlockList) error {
	highestQC := cbft.state.HighestQCBlock()

	if highestQC.NumberU64() > msg.BlockNumber+3 ||
		(highestQC.Hash() == msg.BlockHash && highestQC.NumberU64() == msg.BlockNumber) {
		cbft.log.Debug(fmt.Sprintf("Receive GetQCBlockList failed, local.highestQC:%s,%d, msg:%s", highestQC.Hash().TerminalString(), highestQC.NumberU64(), msg.String()))
		return fmt.Errorf("peer state too low")
	}

	lock := cbft.state.HighestLockBlock()
	commit := cbft.state.HighestCommitBlock()

	qcs := make([]*ctypes.QuorumCert, 0)
	blocks := make([]*types.Block, 0)

	if commit.ParentHash() == msg.BlockHash {
		block, qc := cbft.blockTree.FindBlockAndQC(commit.Hash(), commit.NumberU64())
		qcs = append(qcs, qc)
		blocks = append(blocks, block)
	}

	if lock.ParentHash() == msg.BlockHash || commit.ParentHash() == msg.BlockHash {
		block, qc := cbft.blockTree.FindBlockAndQC(lock.Hash(), lock.NumberU64())
		qcs = append(qcs, qc)
		blocks = append(blocks, block)
	}
	if highestQC.ParentHash() == msg.BlockHash || lock.ParentHash() == msg.BlockHash || commit.ParentHash() == msg.BlockHash {
		block, qc := cbft.blockTree.FindBlockAndQC(highestQC.Hash(), highestQC.NumberU64())
		qcs = append(qcs, qc)
		blocks = append(blocks, block)
	}

	if len(qcs) != 0 {
		cbft.network.Send(id, &protocols.QCBlockList{QC: qcs, Blocks: blocks})
		cbft.log.Debug("Send QCBlockList", "len", len(qcs))
	}
	return nil
}

// OnGetPrepareVote is responsible for processing the business logic
// of the GetPrepareVote message. It will synchronously return a
// PrepareVotes message to the sender.
func (cbft *Cbft) OnGetPrepareVote(id string, msg *protocols.GetPrepareVote) error {
	cbft.log.Debug("Received message on OnGetPrepareVote", "from", id, "msgHash", msg.MsgHash(), "message", msg.String())
	if msg.Epoch == cbft.state.Epoch() && msg.ViewNumber == cbft.state.ViewNumber() {
		prepareVoteMap := cbft.state.AllPrepareVoteByIndex(msg.BlockIndex)
		// Defining an array for receiving PrepareVote.
		votes := make([]*protocols.PrepareVote, 0, len(prepareVoteMap))
		if prepareVoteMap != nil {
			for k, v := range prepareVoteMap {
				if msg.UnKnownSet.GetIndex(k) {
					votes = append(votes, v)
				}
			}
		}
		if len(votes) > 0 {
			cbft.network.Send(id, &protocols.PrepareVotes{Epoch: msg.Epoch, ViewNumber: msg.ViewNumber, BlockIndex: msg.BlockIndex, Votes: votes})
			cbft.log.Debug("Send PrepareVotes", "peer", id, "epoch", msg.Epoch, "viewNumber", msg.ViewNumber, "blockIndex", msg.BlockIndex)
		}
	}
	return nil
}

// OnPrepareVotes handling response from GetPrepareVote response.
func (cbft *Cbft) OnPrepareVotes(id string, msg *protocols.PrepareVotes) error {
	cbft.log.Debug("Received message on OnPrepareVotes", "from", id, "msgHash", msg.MsgHash(), "message", msg.String())
	for _, vote := range msg.Votes {
		if err := cbft.OnPrepareVote(id, vote); err != nil {
			if e, ok := err.(HandleError); ok && e.AuthFailed() {
				cbft.log.Error("OnPrepareVotes failed", "peer", id, "err", err)
			}
			return err
		}
	}
	return nil
}

// OnGetLatestStatus hands GetLatestStatus messages.
//
// main logic:
// 1.Compare the blockNumber of the sending node with the local node,
// and if the blockNumber of local node is larger then reply LatestStatus message,
// the message contains the status information of the local node.
func (cbft *Cbft) OnGetLatestStatus(id string, msg *protocols.GetLatestStatus) error {
	cbft.log.Debug("Received message on OnGetLatestStatus", "from", id, "logicType", msg.LogicType, "msgHash", msg.MsgHash(), "message", msg.String())
	// Define a function that performs the send action.
	launcher := func(bType uint64, targetId string, blockNumber uint64, blockHash common.Hash) error {
		err := cbft.network.PeerSetting(targetId, bType, blockNumber)
		if err != nil {
			cbft.log.Error("GetPeer failed", "err", err, "peerId", targetId)
			return err
		}
		// Synchronize block data with fetchBlock.
		cbft.fetchBlock(targetId, blockHash, blockNumber)
		return nil
	}
	//
	if msg.LogicType == network.TypeForQCBn {
		localQCNum, localQCHash := cbft.state.HighestQCBlock().NumberU64(), cbft.state.HighestQCBlock().Hash()
		if localQCNum == msg.BlockNumber && localQCHash == msg.BlockHash {
			cbft.log.Debug("Local qcBn is equal the sender's qcBn", "remoteBn", msg.BlockNumber, "localBn", localQCNum, "remoteHash", msg.BlockHash, "localHash", localQCHash)
			return nil
		}
		if localQCNum < msg.BlockNumber || (localQCNum == msg.BlockNumber && localQCHash != msg.BlockHash) {
			cbft.log.Debug("Local qcBn is less than the sender's qcBn", "remoteBn", msg.BlockNumber, "localBn", localQCNum)
			return launcher(msg.LogicType, id, msg.BlockNumber, msg.BlockHash)
		}
		cbft.log.Debug("Local qcBn is larger than the sender's qcBn", "remoteBn", msg.BlockNumber, "localBn", localQCNum)
		cbft.network.Send(id, &protocols.LatestStatus{BlockNumber: localQCNum, BlockHash: localQCHash, LogicType: msg.LogicType})
	}
	return nil
}

// OnLatestStatus is used to process LatestStatus messages that received from peer.
func (cbft *Cbft) OnLatestStatus(id string, msg *protocols.LatestStatus) error {
	cbft.log.Debug("Received message on OnLatestStatus", "from", id, "msgHash", msg.MsgHash(), "message", msg.String())
	switch msg.LogicType {
	case network.TypeForQCBn:
		localQCBn, localQCHash := cbft.state.HighestQCBlock().NumberU64(), cbft.state.HighestQCBlock().Hash()
		if localQCBn < msg.BlockNumber || (localQCBn == msg.BlockNumber && localQCHash != msg.BlockHash) {
			err := cbft.network.PeerSetting(id, msg.LogicType, msg.BlockNumber)
			if err != nil {
				cbft.log.Error("PeerSetting failed", "err", err)
				return err
			}
			cbft.log.Debug("LocalQCBn is lower than sender's", "localBn", localQCBn, "remoteBn", msg.BlockNumber)
			cbft.fetchBlock(id, msg.BlockHash, msg.BlockNumber)
		}
	}
	return nil
}

// OnPrepareBlockHash responsible for handling PrepareBlockHash message.
//
// Note: After receiving the PrepareBlockHash message, it is determined whether the
// block information exists locally. If not, send a network request to get
// the block data.
func (cbft *Cbft) OnPrepareBlockHash(id string, msg *protocols.PrepareBlockHash) error {
	cbft.log.Debug("Received message on OnPrepareBlockHash", "from", id, "msgHash", msg.MsgHash(), "message", msg.String())
	if msg.Epoch == cbft.state.Epoch() && msg.ViewNumber == cbft.state.ViewNumber() {
		block := cbft.state.ViewBlockByIndex(msg.BlockIndex)
		if block == nil {
			cbft.network.RemoveMessageHash(id, msg.MsgHash())
			cbft.SyncPrepareBlock(id, msg.Epoch, msg.ViewNumber, msg.BlockIndex)
		}
	}
	return nil
}

// OnGetViewChange responds to nodes that require viewChange.
//
// The Epoch and viewNumber of viewChange must be consistent
// with the state of the current node.
func (cbft *Cbft) OnGetViewChange(id string, msg *protocols.GetViewChange) error {
	cbft.log.Debug("Received message on OnGetViewChange", "from", id, "msgHash", msg.MsgHash(), "message", msg.String(), "local", cbft.state.ViewString())

	localEpoch, localViewNumber := cbft.state.Epoch(), cbft.state.ViewNumber()

	isEqualLocalView := func() bool {
		return msg.ViewNumber == localViewNumber && msg.Epoch == localEpoch
	}

	isLastView := func() bool {
		return msg.ViewNumber+1 == localViewNumber || (msg.Epoch+1 == localEpoch && localViewNumber == state.DefaultViewNumber)
	}

	isPreviousView := func() bool {
		return msg.Epoch == localEpoch && msg.ViewNumber+1 < localViewNumber
	}

	if isEqualLocalView() {
		viewChanges := cbft.state.AllViewChange()

		vcs := &protocols.ViewChanges{}
		for k, v := range viewChanges {
			if msg.ViewChangeBits.GetIndex(k) {
				vcs.VCs = append(vcs.VCs, v)
			}
		}
		cbft.log.Debug("Send ViewChanges", "peer", id, "len", len(vcs.VCs))
		if len(vcs.VCs) != 0 {
			cbft.network.Send(id, vcs)
		}
		return nil
	}
	// Return view QC in the case of less than 1.
	if isLastView() {
		lastViewChangeQC := cbft.state.LastViewChangeQC()
		if lastViewChangeQC == nil {
			cbft.log.Error("Not found lastViewChangeQC")
			return nil
		}
		err := lastViewChangeQC.EqualAll(msg.Epoch, msg.ViewNumber)
		if err != nil {
			cbft.log.Error("Last view change is not equal msg.viewNumber", "err", err)
			return err
		}
		cbft.network.Send(id, &protocols.ViewChangeQuorumCert{
			ViewChangeQC: lastViewChangeQC,
		})
		return nil
	}
	// get previous viewChangeQC from wal db
	if isPreviousView() {
		if qc, err := cbft.bridge.GetViewChangeQC(msg.Epoch, msg.ViewNumber); err == nil && qc != nil {
			cbft.network.Send(id, &protocols.ViewChangeQuorumCert{
				ViewChangeQC: qc,
			})
			return nil
		}
	}

	return fmt.Errorf("request is not match local view, local:%s,msg:%s", cbft.state.ViewString(), msg.String())
}

// OnViewChangeQuorumCert handles the message type of ViewChangeQuorumCertMsg.
func (cbft *Cbft) OnViewChangeQuorumCert(id string, msg *protocols.ViewChangeQuorumCert) error {
	cbft.log.Debug("Received message on OnViewChangeQuorumCert", "from", id, "msgHash", msg.MsgHash(), "message", msg.String())
	viewChangeQC := msg.ViewChangeQC
	epoch, viewNumber, _, _, _, _ := viewChangeQC.MaxBlock()
	if cbft.state.Epoch() == epoch && cbft.state.ViewNumber() == viewNumber {
		if err := cbft.verifyViewChangeQC(msg.ViewChangeQC); err == nil {
			cbft.tryChangeViewByViewChange(msg.ViewChangeQC)
		} else {
			cbft.log.Debug("Verify ViewChangeQC failed", "err", err)
			return &authFailedError{err}
		}
	}
	return nil
}

// OnViewChanges handles the message type of ViewChangesMsg.
func (cbft *Cbft) OnViewChanges(id string, msg *protocols.ViewChanges) error {
	cbft.log.Debug("Received message on OnViewChanges", "from", id, "msgHash", msg.MsgHash(), "message", msg.String())
	for _, v := range msg.VCs {
		if err := cbft.OnViewChange(id, v); err != nil {
			if e, ok := err.(HandleError); ok && e.AuthFailed() {
				cbft.log.Error("OnViewChanges failed", "peer", id, "err", err)
			}
			return err
		}
	}
	return nil
}

// MissingViewChangeNodes returns the node ID of the missing vote.
//
// Notes:
// Use the channel to complete serial execution to prevent concurrency.
func (cbft *Cbft) MissingViewChangeNodes() (v *protocols.GetViewChange, err error) {
	result := make(chan struct{})

	cbft.asyncCallCh <- func() {
		defer func() { result <- struct{}{} }()
		allViewChange := cbft.state.AllViewChange()

		length := cbft.currentValidatorLen()
		vbits := utils.NewBitArray(uint32(length))

		// enough qc or did not reach deadline
		if len(allViewChange) >= cbft.threshold(length) || !cbft.state.IsDeadline() {
			v, err = nil, fmt.Errorf("no need sync viewchange")
			return
		}
		for i := uint32(0); i < vbits.Size(); i++ {
			if _, ok := allViewChange[i]; !ok {
				vbits.SetIndex(i, true)
			}
		}

		v, err = &protocols.GetViewChange{
			Epoch:          cbft.state.Epoch(),
			ViewNumber:     cbft.state.ViewNumber(),
			ViewChangeBits: vbits,
		}, nil
	}
	<-result
	return
}

// MissingPrepareVote returns missing vote.
func (cbft *Cbft) MissingPrepareVote() (v *protocols.GetPrepareVote, err error) {
	result := make(chan struct{})

	cbft.asyncCallCh <- func() {
		defer func() { result <- struct{}{} }()

		begin := cbft.state.MaxQCIndex() + 1
		end := cbft.state.NextViewBlockIndex()
		len := cbft.currentValidatorLen()
		cbft.log.Debug("MissingPrepareVote", "epoch", cbft.state.Epoch(), "viewNumber", cbft.state.ViewNumber(), "beginIndex", begin, "endIndex", end, "validatorLen", len)

		for i := begin; i < end; i++ {
			size := cbft.state.PrepareVoteLenByIndex(i)
			cbft.log.Debug("The length of prepare vote", "index", i, "size", size)

			if size < cbft.threshold(len) { // need sync prepare votes
				knownVotes := cbft.state.AllPrepareVoteByIndex(i)
				unKnownSet := utils.NewBitArray(uint32(len))
				for i := uint32(0); i < unKnownSet.Size(); i++ {
					if _, ok := knownVotes[i]; !ok {
						unKnownSet.SetIndex(i, true)
					}
				}

				v, err = &protocols.GetPrepareVote{
					Epoch:      cbft.state.Epoch(),
					ViewNumber: cbft.state.ViewNumber(),
					BlockIndex: i,
					UnKnownSet: unKnownSet,
				}, nil
				break
			}
		}
		if v == nil {
			err = fmt.Errorf("not need sync prepare vote")
		}
	}
	<-result
	return
}

// OnPong is used to receive the average delay time.
func (cbft *Cbft) OnPong(nodeID string, netLatency int64) error {
	cbft.log.Trace("OnPong", "nodeID", nodeID, "netLatency", netLatency)
	cbft.netLatencyLock.Lock()
	defer cbft.netLatencyLock.Unlock()
	latencyList, exist := cbft.netLatencyMap[nodeID]
	if !exist {
		cbft.netLatencyMap[nodeID] = list.New()
		cbft.netLatencyMap[nodeID].PushBack(netLatency)
	} else {
		if latencyList.Len() > 5 {
			e := latencyList.Front()
			cbft.netLatencyMap[nodeID].Remove(e)
		}
		cbft.netLatencyMap[nodeID].PushBack(netLatency)
	}
	return nil
}

// BlockExists is used to query whether the specified block exists in this node.
func (cbft *Cbft) BlockExists(blockNumber uint64, blockHash common.Hash) error {
	result := make(chan error, 1)
	cbft.asyncCallCh <- func() {
		if (blockHash == common.Hash{}) {
			result <- fmt.Errorf("invalid blockHash")
			return
		}
		block := cbft.blockTree.FindBlockByHash(blockHash)
		if block = cbft.blockChain.GetBlock(blockHash, blockNumber); block == nil {
			result <- fmt.Errorf("not found block by hash:%s, number:%d", blockHash.TerminalString(), blockNumber)
			return
		}
		if block.Hash() != blockHash || blockNumber != block.NumberU64() {
			result <- fmt.Errorf("not match from block, hash:%s, number:%d, queriedHash:%s, queriedNumber:%d",
				blockHash.TerminalString(), blockNumber,
				block.Hash().TerminalString(), block.NumberU64())
			return
		}
		result <- nil
	}
	return <-result
}

// AvgLatency returns the average delay time of the specified node.
//
// The average is the average delay between the current
// node and all consensus nodes.
// Return value unit: milliseconds.
func (cbft *Cbft) AvgLatency() time.Duration {
	cbft.netLatencyLock.Lock()
	defer cbft.netLatencyLock.Unlock()
	// The intersection of peerSets and consensusNodes.
	target, _ := cbft.network.AliveConsensusNodeIDs()
	var (
		avgSum     int64
		result     int64
		validCount int64
	)
	// Take 2/3 nodes from the target.
	var pair utils.KeyValuePairList
	for _, v := range target {
		if latencyList, exist := cbft.netLatencyMap[v]; exist {
			avg := calAverage(latencyList)
			pair.Push(utils.KeyValuePair{Key: v, Value: avg})
		}
	}
	sort.Sort(pair)
	if pair.Len() == 0 {
		return time.Duration(0)
	}
	validCount = int64(pair.Len() * 2 / 3)
	if validCount == 0 {
		validCount = 1
	}
	for _, v := range pair[:validCount] {
		avgSum += v.Value
	}

	result = avgSum / validCount
	cbft.log.Debug("Get avg latency", "avg", result)
	return time.Duration(result) * time.Millisecond
}

// DefaultAvgLatency returns the avg latency of default.
func (cbft *Cbft) DefaultAvgLatency() time.Duration {
	return time.Duration(protocols.DefaultAvgLatency) * time.Millisecond
}

func calAverage(latencyList *list.List) int64 {
	var (
		sum    int64
		counts int64
	)
	for e := latencyList.Front(); e != nil; e = e.Next() {
		if latency, ok := e.Value.(int64); ok {
			counts++
			sum += latency
		}
	}
	if counts > 0 {
		return sum / counts
	}
	return 0
}

func (cbft *Cbft) SyncPrepareBlock(id string, epoch uint64, viewNumber uint64, blockIndex uint32) {
	if cbft.syncingCache.AddOrReplace(blockIndex) {
		msg := &protocols.GetPrepareBlock{Epoch: epoch, ViewNumber: viewNumber, BlockIndex: blockIndex}
		cbft.network.Send(id, msg)
		cbft.log.Debug("Send GetPrepareBlock", "peer", id, "msg", msg.String())
	}
}

func (cbft *Cbft) SyncBlockQuorumCert(id string, blockNumber uint64, blockHash common.Hash) {
	if cbft.syncingCache.AddOrReplace(blockHash) {
		msg := &protocols.GetBlockQuorumCert{BlockHash: blockHash, BlockNumber: blockNumber}
		cbft.network.Send(id, msg)
		cbft.log.Debug("Send GetBlockQuorumCert", "peer", id, "msg", msg.String())
	}

}