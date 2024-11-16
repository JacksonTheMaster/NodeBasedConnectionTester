// config.go
package main

import (
	"os"

	"gopkg.in/yaml.v2"
)

type Config struct {
	NodeID     string   `yaml:"nodeId"`
	ListenAddr string   `yaml:"listenAddr"` // UDP discovery address
	IperfPort  int      `yaml:"iperfPort"`  // TCP port for iperf server
	KnownPeers []string `yaml:"knownPeers"` // UDP discovery addresses
	DataFile   string   `yaml:"dataFile"`
	TestConfig struct {
		IperfInterval   int    `yaml:"iperfInterval"`   // seconds
		MLabInterval    int    `yaml:"mlabInterval"`    // seconds
		TargetBandwidth string `yaml:"targetBandwidth"` // e.g., "1Gbps"
	} `yaml:"testConfig"`
}

func LoadConfig(filename string) (*Config, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Set defaults
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = ":7777"
	}
	if cfg.IperfPort == 0 {
		cfg.IperfPort = 5201 // default iperf3 port
	}
	if cfg.TestConfig.IperfInterval == 0 {
		cfg.TestConfig.IperfInterval = 300 // 5 minutes
	}
	if cfg.TestConfig.MLabInterval == 0 {
		cfg.TestConfig.MLabInterval = 1800 // 30 minutes
	}
	if cfg.DataFile == "" {
		cfg.DataFile = "testdata.json"
	}

	return cfg, nil
}
