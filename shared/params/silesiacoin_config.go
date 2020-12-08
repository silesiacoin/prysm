package params

import "strings"

// UseSSCNetworkConfig uses the Silesiacoin (SSC) specific
// network config.
func UseSSCNetworkConfig() {
	cfg := BeaconNetworkConfig().Copy()
	cfg.ContractDeploymentBlock = 1673616
	cfg.ChainID = 4004180
	cfg.NetworkID = 4004180
	cfg.DepositContractAddress = strings.ToLower("0x3821049BD07cB4dA46b9898119DE6EAE0553e0c3")
	cfg.BootstrapNodes = []string{
		//"enr:-Ku4QOA5OGWObY8ep_x35NlGBEj7IuQULTjkgxC_0G1AszqGEA0Wn2RNlyLFx9zGTNB1gdFBA6ZDYxCgIza1uJUUOj4Dh2F0dG5ldHOIAAAAAAAAAACEZXRoMpDVTPWXAAAgCf__________gmlkgnY0gmlwhDQPSjiJc2VjcDI1NmsxoQM6yTQB6XGWYJbI7NZFBjp4Yb9AYKQPBhVrfUclQUobb4N1ZHCCIyg",
		//"enr:-Ku4QOksdA2tabOGrfOOr6NynThMoio6Ggka2oDPqUuFeWCqcRM2alNb8778O_5bK95p3EFt0cngTUXm2H7o1jkSJ_8Dh2F0dG5ldHOIAAAAAAAAAACEZXRoMpDVTPWXAAAgCf__________gmlkgnY0gmlwhDaa13aJc2VjcDI1NmsxoQKdNQJvnohpf0VO0ZYCAJxGjT0uwJoAHbAiBMujGjK0SoN1ZHCCIyg",
	}
	OverrideBeaconNetworkConfig(cfg)
}

// UseLSSCConfig sets the main beacon chain
// config for Silesiacoin.
func UseLSSCConfig() {
	beaconConfig = SscConfig()
}

// L14 config defines the config for the
// L14 testnet.
func SscConfig() *BeaconChainConfig {
	cfg := MainnetConfig().Copy()
	// TODO: you've got to pump this numbers up
	cfg.MinGenesisActiveValidatorCount = 2
	// Dec 1, 2020, 12pm UTC.
	cfg.MinGenesisTime = 1606824000
	cfg.GenesisDelay = 0
	cfg.NetworkName = "ssc"
	cfg.GenesisDelay = 0
	cfg.SecondsPerETH1Block = 14
	return cfg
}
