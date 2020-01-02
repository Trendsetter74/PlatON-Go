package crossContractCall;

import beforetest.ContractPrepareTest;
import network.platon.autotest.junit.annotations.DataSource;
import network.platon.autotest.junit.enums.DataSourceType;
import network.platon.contracts.Callee0425;
import network.platon.contracts.Caller0425;
import network.platon.contracts.DelegatecallCallee_050;
import network.platon.contracts.DelegatecallCaller_050;
import org.junit.Before;
import org.junit.Test;
import org.web3j.protocol.core.methods.response.TransactionReceipt;


/**
 * @title 0.4.25跨合约调用的调用者
 *  说明：CALL修改的是被调用者的状态变量，使用的是上一个调用者地址
 *       DELEGATECALL会一直使用原始调用者的地址，而CALLCODE不会。两者都是修改被调用者的状态
 * @description:
 * @author: hudenian
 * @create: 2019/12/30
 */
public class Caller0425Test extends ContractPrepareTest {

    @Before
    public void before() {
        this.prepare();
    }


    @Test
    @DataSource(type = DataSourceType.EXCEL, file = "test.xls", sheetName = "Sheet1",
            author = "hudenian", showName = "Caller0425Test-0.4.25跨合约调用CALL")
    public void caller0425CallTest() {
        try {
            //调用者合约地址
            Caller0425 caller0425 = Caller0425.deploy(web3j, transactionManager, provider).send();
            String callerContractAddress = caller0425.getContractAddress();
            TransactionReceipt tx = caller0425.getTransactionReceipt().get();
            collector.logStepPass("Caller0425 deploy successfully.contractAddress:" + callerContractAddress + ", hash:" + tx.getTransactionHash());


            //被调用者合约地址
            Callee0425 callee0425 = Callee0425.deploy(web3j, transactionManager, provider).send();
            String calleeContractAddress = callee0425.getContractAddress();
            TransactionReceipt tx1 = callee0425.getTransactionReceipt().get();
            collector.logStepPass("Callee0425 deploy successfully.contractAddress:" + calleeContractAddress + ", hash:" + tx1.getTransactionHash());

            //查询调用者x值
            String callerX = caller0425.getCallerX().send().toString();
            collector.logStepPass("Caller0425 合约中X的值为："+callerX);

            //查询被调用者x值
            String calleeX = callee0425.getCalleeX().send().toString();
            collector.logStepPass("Callee0425 合约中X的值为："+calleeX);


            TransactionReceipt tx2 = caller0425.inc_call(calleeContractAddress).send();
            collector.logStepPass("执行跨合约调用后，hash:" + tx2.getTransactionHash());

            //查询调用者x值
            String callerAfterX = caller0425.getCallerX().send().toString();
            collector.logStepPass("跨合约调用后，Caller0425 合约中X的值为："+callerAfterX);

            //查询被调用者x值
            String calleeAfterX = callee0425.getCalleeX().send().toString();
            collector.logStepPass("跨合约调用后，Callee0425 合约中X的值为："+calleeAfterX);


        } catch (Exception e) {
            e.printStackTrace();
        }
    }


    @Test
    @DataSource(type = DataSourceType.EXCEL, file = "test1.xls", sheetName = "Sheet1",
            author = "hudenian", showName = "Caller0425Test-0.4.25跨合约调用CALLCODE")
    public void caller0425CallCodeTest() {
        try {
            //调用者合约地址
            Caller0425 caller0425 = Caller0425.deploy(web3j, transactionManager, provider).send();
            String callerContractAddress = caller0425.getContractAddress();
            TransactionReceipt tx = caller0425.getTransactionReceipt().get();
            collector.logStepPass("Caller0425 deploy successfully.contractAddress:" + callerContractAddress + ", hash:" + tx.getTransactionHash());


            //被调用者合约地址
            Callee0425 callee0425 = Callee0425.deploy(web3j, transactionManager, provider).send();
            String calleeContractAddress = callee0425.getContractAddress();
            TransactionReceipt tx1 = callee0425.getTransactionReceipt().get();
            collector.logStepPass("Callee0425 deploy successfully.contractAddress:" + calleeContractAddress + ", hash:" + tx1.getTransactionHash());

            //查询调用者x值
            String callerX = caller0425.getCallerX().send().toString();
            collector.logStepPass("Caller0425 合约中X的值为："+callerX);

            //查询被调用者x值
            String calleeX = callee0425.getCalleeX().send().toString();
            collector.logStepPass("Callee0425 合约中X的值为："+calleeX);


            TransactionReceipt tx2 = caller0425.inc_callcode(calleeContractAddress).send();
            collector.logStepPass("执行跨合约调用后，hash:" + tx2.getTransactionHash());

            //查询调用者x值
            String callerAfterX = caller0425.getCallerX().send().toString();
            collector.logStepPass("跨合约调用后，Caller0425 合约中X的值为："+callerAfterX);

            //查询被调用者x值
            String calleeAfterX = callee0425.getCalleeX().send().toString();
            collector.logStepPass("跨合约调用后，Callee0425 合约中X的值为："+calleeAfterX);


        } catch (Exception e) {
            e.printStackTrace();
        }
    }


    @Test
    @DataSource(type = DataSourceType.EXCEL, file = "test2.xls", sheetName = "Sheet1",
            author = "hudenian", showName = "Caller0425Test-0.4.25跨合约调用DELEGATECALL")
    public void caller0425DelegateCallTest() {
        try {
            //调用者合约地址
            Caller0425 caller0425 = Caller0425.deploy(web3j, transactionManager, provider).send();
            String callerContractAddress = caller0425.getContractAddress();
            TransactionReceipt tx = caller0425.getTransactionReceipt().get();
            collector.logStepPass("Caller0425 deploy successfully.contractAddress:" + callerContractAddress + ", hash:" + tx.getTransactionHash());


            //被调用者合约地址
            Callee0425 callee0425 = Callee0425.deploy(web3j, transactionManager, provider).send();
            String calleeContractAddress = callee0425.getContractAddress();
            TransactionReceipt tx1 = callee0425.getTransactionReceipt().get();
            collector.logStepPass("Callee0425 deploy successfully.contractAddress:" + calleeContractAddress + ", hash:" + tx1.getTransactionHash());

            //查询调用者x值
            String callerX = caller0425.getCallerX().send().toString();
            collector.logStepPass("Caller0425 合约中X的值为："+callerX);

            //查询被调用者x值
            String calleeX = callee0425.getCalleeX().send().toString();
            collector.logStepPass("Callee0425 合约中X的值为："+calleeX);


            TransactionReceipt tx2 = caller0425.inc_delegatecall(calleeContractAddress).send();
            collector.logStepPass("执行跨合约调用后，hash:" + tx2.getTransactionHash());

            //查询调用者x值
            String callerAfterX = caller0425.getCallerX().send().toString();
            collector.logStepPass("跨合约调用后，Caller0425 合约中X的值为："+callerAfterX);

            //查询被调用者x值
            String calleeAfterX = callee0425.getCalleeX().send().toString();
            collector.logStepPass("跨合约调用后，Callee0425 合约中X的值为："+calleeAfterX);


        } catch (Exception e) {
            e.printStackTrace();
        }
    }

}