package aggregator

import (
	"context"
	"sync"
	"time"

	"github.com/zees-dev/blockless-avs/aggregator/types"
	cstaskmanager "github.com/zees-dev/blockless-avs/contracts/bindings/IncredibleSquaringTaskManager"
	"github.com/zees-dev/blockless-avs/core"
	"github.com/zees-dev/blockless-avs/core/chainio"
	"github.com/zees-dev/blockless-avs/core/config"

	"github.com/Layr-Labs/eigensdk-go/chainio/clients"
	"github.com/Layr-Labs/eigensdk-go/logging"
	"github.com/Layr-Labs/eigensdk-go/services/avsregistry"
	blsagg "github.com/Layr-Labs/eigensdk-go/services/bls_aggregation"
	oprsinfoserv "github.com/Layr-Labs/eigensdk-go/services/operatorsinfo"
	sdktypes "github.com/Layr-Labs/eigensdk-go/types"
)

const (
	// number of blocks after which a task is considered expired
	// this hardcoded here because it's also hardcoded in the contracts, but should
	// ideally be fetched from the contracts
	taskChallengeWindowBlock = 100
	blockTimeSeconds         = 12 * time.Second
	avsName                  = "incredible-squaring"
)

// Aggregator sends tasks (numbers to square) onchain, then listens for operator signed TaskResponses.
// It aggregates responses signatures, and if any of the TaskResponses reaches the QuorumThresholdPercentage for each quorum
// (currently we only use a single quorum of the ERC20Mock token), it sends the aggregated TaskResponse and signature onchain.
//
// The signature is checked in the BLSSignatureChecker.sol contract, which expects a
//
//	struct NonSignerStakesAndSignature {
//		uint32[] nonSignerQuorumBitmapIndices;
//		BN254.G1Point[] nonSignerPubkeys;
//		BN254.G1Point[] quorumApks;
//		BN254.G2Point apkG2;
//		BN254.G1Point sigma;
//		uint32[] quorumApkIndices;
//		uint32[] totalStakeIndices;
//		uint32[][] nonSignerStakeIndices; // nonSignerStakeIndices[quorumNumberIndex][nonSignerIndex]
//	}
//
// A task can only be responded onchain by having enough operators sign on it such that their stake in each quorum reaches the QuorumThresholdPercentage.
// In order to verify this onchain, the Registry contracts store the history of stakes and aggregate pubkeys (apks) for each operators and each quorum. These are
// updated everytime an operator registers or deregisters with the BLSRegistryCoordinatorWithIndices.sol contract, or calls UpdateStakes() on the StakeRegistry.sol contract,
// after having received new delegated shares or having delegated shares removed by stakers queuing withdrawals. Each of these pushes to their respective datatype array a new entry.
//
// This is true for quorumBitmaps (represent the quorums each operator is opted into), quorumApks (apks per quorum), totalStakes (total stake per quorum), and nonSignerStakes (stake per quorum per operator).
// The 4 "indices" in NonSignerStakesAndSignature basically represent the index at which to fetch their respective data, given a blockNumber at which the task was created.
// Note that different data types might have different indices, since for eg QuorumBitmaps are updated for operators registering/deregistering, but not for UpdateStakes.
// Thankfully, we have deployed a helper contract BLSOperatorStateRetriever.sol whose function getCheckSignaturesIndices() can be used to fetch the indices given a block number.
//
// The 4 other fields nonSignerPubkeys, quorumApks, apkG2, and sigma, however, must be computed individually.
// apkG2 and sigma are just the aggregated signature and pubkeys of the operators who signed the task response (aggregated over all quorums, so individual signatures might be duplicated).
// quorumApks are the G1 aggregated pubkeys of the operators who signed the task response, but one per quorum, as opposed to apkG2 which is summed over all quorums.
// nonSignerPubkeys are the G1 pubkeys of the operators who did not sign the task response, but were opted into the quorum at the blocknumber at which the task was created.
// Upon sending a task onchain (or receiving a NewTaskCreated Event if the tasks were sent by an external task generator), the aggregator can get the list of all operators opted into each quorum at that
// block number by calling the getOperatorState() function of the BLSOperatorStateRetriever.sol contract.
type Aggregator struct {
	logger           logging.Logger
	serverIpPortAddr string
	avsWriter        chainio.AvsWriterer
	avsSubscriber    chainio.AvsSubscriberer
	// aggregation related fields
	blsAggregationService blsagg.BlsAggregationService
	// receive new tasks in this chan (typically from listening to onchain event)
	newTaskCreatedChan   chan *cstaskmanager.ContractIncredibleSquaringTaskManagerNewTaskCreated
	newTaskRespondedChan chan *cstaskmanager.ContractIncredibleSquaringTaskManagerTaskResponded
	tasks                map[types.TaskIndex]cstaskmanager.IIncredibleSquaringTaskManagerTask
	tasksMu              sync.RWMutex
	taskResponses        map[types.TaskIndex]map[sdktypes.TaskResponseDigest]cstaskmanager.IIncredibleSquaringTaskManagerTaskResponse
	taskResponsesMu      sync.RWMutex
}

// NewAggregator creates a new Aggregator with the provided config.
func NewAggregator(c *config.Config) (*Aggregator, error) {

	avsReader, err := chainio.BuildAvsReaderFromConfig(c)
	if err != nil {
		c.Logger.Error("Cannot create avsReader", "err", err)
		return nil, err
	}

	avsWriter, err := chainio.BuildAvsWriterFromConfig(c)
	if err != nil {
		c.Logger.Errorf("Cannot create avsWriter", "err", err)
		return nil, err
	}
	avsSubscriber, err := chainio.BuildAvsSubscriberFromConfig(c)
	if err != nil {
		c.Logger.Errorf("Cannot create avsSubscriber", "err", err)
		return nil, err
	}

	chainioConfig := clients.BuildAllConfig{
		EthHttpUrl:                 c.EthHttpRpcUrl,
		EthWsUrl:                   c.EthWsRpcUrl,
		RegistryCoordinatorAddr:    c.IncredibleSquaringRegistryCoordinatorAddr.String(),
		OperatorStateRetrieverAddr: c.OperatorStateRetrieverAddr.String(),
		AvsName:                    avsName,
		PromMetricsIpPortAddress:   ":9090",
	}
	clients, err := clients.BuildAll(chainioConfig, c.EcdsaPrivateKey, c.Logger)
	if err != nil {
		c.Logger.Errorf("Cannot create sdk clients", "err", err)
		return nil, err
	}

	operatorPubkeysService := oprsinfoserv.NewOperatorsInfoServiceInMemory(context.Background(), clients.AvsRegistryChainSubscriber, clients.AvsRegistryChainReader, c.Logger)
	avsRegistryService := avsregistry.NewAvsRegistryServiceChainCaller(avsReader, operatorPubkeysService, c.Logger)
	blsAggregationService := blsagg.NewBlsAggregatorService(avsRegistryService, c.Logger)

	return &Aggregator{
		logger:                c.Logger,
		serverIpPortAddr:      c.AggregatorServerIpPortAddr,
		avsWriter:             avsWriter,
		avsSubscriber:         avsSubscriber,
		blsAggregationService: blsAggregationService,
		newTaskCreatedChan:    make(chan *cstaskmanager.ContractIncredibleSquaringTaskManagerNewTaskCreated),
		newTaskRespondedChan:  make(chan *cstaskmanager.ContractIncredibleSquaringTaskManagerTaskResponded),
		tasks:                 make(map[types.TaskIndex]cstaskmanager.IIncredibleSquaringTaskManagerTask),
		taskResponses:         make(map[types.TaskIndex]map[sdktypes.TaskResponseDigest]cstaskmanager.IIncredibleSquaringTaskManagerTaskResponse),
	}, nil
}

func (agg *Aggregator) Start(ctx context.Context) error {
	agg.logger.Infof("Starting aggregator")
	agg.logger.Infof("Starting aggregator rpc server.")
	go agg.startServer(ctx)

	subTaskCreated := agg.avsSubscriber.SubscribeToNewTasks(agg.newTaskCreatedChan)
	subTaskResponded := agg.avsSubscriber.SubscribeToTaskResponses(agg.newTaskRespondedChan)
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-subTaskCreated.Err():
			agg.logger.Error("Error in websocket subscription for NewTaskCreated", "err", err)
			subTaskCreated.Unsubscribe()
			subTaskCreated = agg.avsSubscriber.SubscribeToNewTasks(agg.newTaskCreatedChan)
		case newTaskCreatedLog := <-agg.newTaskCreatedChan:
			agg.logger.Info("Received new task", "newTaskCreatedLog", newTaskCreatedLog)
			// agg.metrics.IncNumTasksReceived()
			if err := agg.processTaskCreatedLog(newTaskCreatedLog); err != nil {
				agg.logger.Error("Failed to process new task", "err", err)
			}
		case err := <-subTaskResponded.Err():
			agg.logger.Error("Error in websocket subscription for TaskResponded", "err", err)
			subTaskResponded.Unsubscribe()
			subTaskResponded = agg.avsSubscriber.SubscribeToTaskResponses(agg.newTaskRespondedChan)
		case taskRespondedLog := <-agg.newTaskRespondedChan:
			agg.logger.Info("Received task response successfully!; taskRespondedLog: %#v", taskRespondedLog)
			// agg.metrics.IncNumTaskResponsesReceived()
		case blsAggServiceResp := <-agg.blsAggregationService.GetResponseChannel():
			agg.logger.Info("Received response from blsAggregationService", "blsAggServiceResp", blsAggServiceResp)
			agg.sendAggregatedResponseToContract(blsAggServiceResp)
		}
	}
}

func (agg *Aggregator) sendAggregatedResponseToContract(blsAggServiceResp blsagg.BlsAggregationServiceResponse) {
	// TODO: check if blsAggServiceResp contains an err
	if blsAggServiceResp.Err != nil {
		agg.logger.Error("BlsAggregationServiceResponse contains an error", "err", blsAggServiceResp.Err)
		// panicing to help with debugging (fail fast), but we shouldn't panic if we run this in production
		panic(blsAggServiceResp.Err)
	}
	nonSignerPubkeys := []cstaskmanager.BN254G1Point{}
	for _, nonSignerPubkey := range blsAggServiceResp.NonSignersPubkeysG1 {
		nonSignerPubkeys = append(nonSignerPubkeys, core.ConvertToBN254G1Point(nonSignerPubkey))
	}
	quorumApks := []cstaskmanager.BN254G1Point{}
	for _, quorumApk := range blsAggServiceResp.QuorumApksG1 {
		quorumApks = append(quorumApks, core.ConvertToBN254G1Point(quorumApk))
	}
	nonSignerStakesAndSignature := cstaskmanager.IBLSSignatureCheckerNonSignerStakesAndSignature{
		NonSignerPubkeys:             nonSignerPubkeys,
		QuorumApks:                   quorumApks,
		ApkG2:                        core.ConvertToBN254G2Point(blsAggServiceResp.SignersApkG2),
		Sigma:                        core.ConvertToBN254G1Point(blsAggServiceResp.SignersAggSigG1.G1Point),
		NonSignerQuorumBitmapIndices: blsAggServiceResp.NonSignerQuorumBitmapIndices,
		QuorumApkIndices:             blsAggServiceResp.QuorumApkIndices,
		TotalStakeIndices:            blsAggServiceResp.TotalStakeIndices,
		NonSignerStakeIndices:        blsAggServiceResp.NonSignerStakeIndices,
	}

	agg.logger.Info("Threshold reached. Sending aggregated response onchain.",
		"taskIndex", blsAggServiceResp.TaskIndex,
	)
	agg.tasksMu.RLock()
	task := agg.tasks[blsAggServiceResp.TaskIndex]
	agg.tasksMu.RUnlock()
	agg.taskResponsesMu.RLock()
	taskResponse := agg.taskResponses[blsAggServiceResp.TaskIndex][blsAggServiceResp.TaskResponseDigest]
	agg.taskResponsesMu.RUnlock()
	_, err := agg.avsWriter.SendAggregatedResponse(context.Background(), task, taskResponse, nonSignerStakesAndSignature)
	if err != nil {
		agg.logger.Error("Aggregator failed to respond to task", "err", err)
	}
}

func (agg *Aggregator) processTaskCreatedLog(taskLog *cstaskmanager.ContractIncredibleSquaringTaskManagerNewTaskCreated) error {
	agg.tasksMu.Lock()
	agg.tasks[taskLog.TaskIndex] = taskLog.Task
	agg.tasksMu.Unlock()

	quorumThresholdPercentages := make(sdktypes.QuorumThresholdPercentages, len(taskLog.Task.QuorumNumbers))
	for i := range taskLog.Task.QuorumNumbers {
		quorumThresholdPercentages[i] = sdktypes.QuorumThresholdPercentage(taskLog.Task.QuorumThresholdPercentage)
	}
	// TODO(samlaf): we use seconds for now, but we should ideally pass a blocknumber to the blsAggregationService
	// and it should monitor the chain and only expire the task aggregation once the chain has reached that block number.
	taskTimeToExpiry := taskChallengeWindowBlock * blockTimeSeconds
	var quorumNums sdktypes.QuorumNums
	for _, quorumNum := range taskLog.Task.QuorumNumbers {
		quorumNums = append(quorumNums, sdktypes.QuorumNum(quorumNum))
	}
	err := agg.blsAggregationService.InitializeNewTask(
		taskLog.TaskIndex,
		taskLog.Task.TaskCreatedBlock,
		quorumNums,
		quorumThresholdPercentages,
		taskTimeToExpiry,
	)
	if err != nil {
		return err
	}
	return nil
}
