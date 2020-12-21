package featureconfig

import (
	"github.com/urfave/cli/v2"
)

var (
	// AltonaTestnet flag for the multiclient eth2 testnet configuration.
	AltonaTestnet = &cli.BoolFlag{
		Name:  "altona",
		Usage: "This defines the flag through which we can run on the Altona Multiclient Testnet",
	}
	// OnyxTestnet flag for the Prysmatic Labs single-client testnet configuration.
	OnyxTestnet = &cli.BoolFlag{
		Name:  "onyx",
		Usage: "This defines the flag through which we can run on the Onyx Prysm Testnet",
	}
	L14TestNet = &cli.BoolFlag{
		Name:  "l14",
		Usage: "This is POA Authority round lukso network",
	}
	// MedallaTestnet flag for the multiclient eth2 testnet.
	MedallaTestnet = &cli.BoolFlag{
		Name:  "medalla",
		Usage: "This defines the flag through which we can run on the Medalla Multiclient Testnet",
	}
	// SpadinaTestnet flag for the multiclient eth2 testnet.
	SpadinaTestnet = &cli.BoolFlag{
		Name:  "spadina",
		Usage: "This defines the flag through which we can run on the Spadina Multiclient Testnet",
	}
	// ZinkenTestnet flag for the multiclient eth2 testnet.
	ZinkenTestnet = &cli.BoolFlag{
		Name:  "zinken",
		Usage: "This defines the flag through which we can run on the Zinken Multiclient Testnet",
	}
	// Mainnet flag for easier tooling, no-op
	Mainnet = &cli.BoolFlag{
		Value: true,
		Name:  "mainnet",
		Usage: "Run on Ethereum 2.0 Main Net. This is the default and can be omitted.",
	}
	devModeFlag = &cli.BoolFlag{
		Name:  "dev",
		Usage: "Enable experimental features still in development. These features may not be stable.",
	}
	writeSSZStateTransitionsFlag = &cli.BoolFlag{
		Name:  "interop-write-ssz-state-transitions",
		Usage: "Write ssz states to disk after attempted state transition",
	}
	kafkaBootstrapServersFlag = &cli.StringFlag{
		Name:  "kafka-url",
		Usage: "Stream attestations and blocks to specified kafka servers. This field is used for bootstrap.servers kafka config field.",
	}
	enableExternalSlasherProtectionFlag = &cli.BoolFlag{
		Name: "enable-external-slasher-protection",
		Usage: "Enables the validator to connect to external slasher to prevent it from " +
			"transmitting a slashable offence over the network.",
	}
	waitForSyncedFlag = &cli.BoolFlag{
		Name:  "wait-for-synced",
		Usage: "Uses WaitForSynced for validator startup, to ensure a validator is able to communicate with the beacon node as quick as possible",
	}
	disableLookbackFlag = &cli.BoolFlag{
		Name:  "disable-lookback",
		Usage: "Disables use of the lookback feature and updates attestation history for validators from head to epoch 0",
	}
	disableGRPCConnectionLogging = &cli.BoolFlag{
		Name:  "disable-grpc-connection-logging",
		Usage: "Disables displaying logs for newly connected grpc clients",
	}
	attestationAggregationStrategy = &cli.StringFlag{
		Name:  "attestation-aggregation-strategy",
		Usage: "Which strategy to use when aggregating attestations, one of: naive, max_cover.",
		Value: "max_cover",
	}
	enableBlst = &cli.BoolFlag{
		Name:  "blst",
		Usage: "Enable new BLS library, blst, from Supranational",
	}
	disableEth1DataMajorityVote = &cli.BoolFlag{
		Name:  "disable-eth1-data-majority-vote",
		Usage: "Disables the Voting With The Majority algorithm when voting for eth1data.",
	}
	disableAccountsV2 = &cli.BoolFlag{
		Name:  "disable-accounts-v2",
		Usage: "Disables usage of v2 for Prysm validator accounts",
	}
	enablePeerScorer = &cli.BoolFlag{
		Name:  "enable-peer-scorer",
		Usage: "Enable experimental P2P peer scorer",
	}
	checkPtInfoCache = &cli.BoolFlag{
		Name:  "use-check-point-cache",
		Usage: "Enables check point info caching",
	}
	disablePruningDepositProofs = &cli.BoolFlag{
		Name: "disable-pruning-deposit-proofs",
		Usage: "Disables pruning deposit proofs when they are no longer needed." +
			"This will probably significantly increase the amount of memory taken up by deposits.",
	}
	enableSyncBacktracking = &cli.BoolFlag{
		Name:  "enable-sync-backtracking",
		Usage: "Enable experimental fork exploration backtracking algorithm",
	}
)

// devModeFlags holds list of flags that are set when development mode is on.
var devModeFlags = []cli.Flag{
	enablePeerScorer,
	enableSyncBacktracking,
}

// ValidatorFlags contains a list of all the feature flags that apply to the validator client.
var ValidatorFlags = append(deprecatedFlags, []cli.Flag{
	enableExternalSlasherProtectionFlag,
	waitForSyncedFlag,
	AltonaTestnet,
	OnyxTestnet,
	MedallaTestnet,
	SpadinaTestnet,
	ZinkenTestnet,
	Mainnet,
	L14TestNet,
	disableAccountsV2,
	enableBlst,
}...)

// SlasherFlags contains a list of all the feature flags that apply to the slasher client.
var SlasherFlags = append(deprecatedFlags, []cli.Flag{
	disableLookbackFlag,
	AltonaTestnet,
	OnyxTestnet,
	MedallaTestnet,
	SpadinaTestnet,
	ZinkenTestnet,
	L14TestNet,
	Mainnet,
}...)

// E2EValidatorFlags contains a list of the validator feature flags to be tested in E2E.
var E2EValidatorFlags = []string{
	"--wait-for-synced",
}

// BeaconChainFlags contains a list of all the feature flags that apply to the beacon-chain client.
var BeaconChainFlags = append(deprecatedFlags, []cli.Flag{
	devModeFlag,
	writeSSZStateTransitionsFlag,
	kafkaBootstrapServersFlag,
	waitForSyncedFlag,
	disableGRPCConnectionLogging,
	attestationAggregationStrategy,
	AltonaTestnet,
	OnyxTestnet,
	MedallaTestnet,
	SpadinaTestnet,
	ZinkenTestnet,
	Mainnet,
	L14TestNet,
	enableBlst,
	disableEth1DataMajorityVote,
	enablePeerScorer,
	checkPtInfoCache,
	disablePruningDepositProofs,
	enableSyncBacktracking,
}...)

// E2EBeaconChainFlags contains a list of the beacon chain feature flags to be tested in E2E.
var E2EBeaconChainFlags = []string{
	"--attestation-aggregation-strategy=max_cover",
	"--dev",
	"--use-check-point-cache",
}
