package fullnode

import (
	"bytes"
	_ "embed"
	"fmt"
	"strconv"

	"github.com/BurntSushi/toml"
	"github.com/peterbourgon/mergemap"
	cosmosv1 "github.com/strangelove-ventures/cosmos-operator/api/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	configOverlayFile = "config-overlay.toml"
	appOverlayFile    = "app-overlay.toml"
)

// ExternalConfig is configuration pulled from kubernetes or other sources.
type ExternalConfig interface {
	P2PExternalAddress() string
}

// BuildConfigMap creates a ConfigMap with configuration to be mounted as files into containers.
// Currently, the config.toml (for Tendermint) and app.toml (for the Cosmos SDK).
func BuildConfigMap(crd *cosmosv1.CosmosFullNode, cfg ExternalConfig) (corev1.ConfigMap, error) {
	cm := corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      appName(crd),
			Namespace: crd.Namespace,
			Labels:    defaultLabels(crd),
		},
	}

	var (
		data = make(map[string]string)
		buf  = new(bytes.Buffer)
	)
	if err := addTendermintToml(buf, data, crd.Spec.ChainConfig, cfg); err != nil {
		return cm, err
	}
	buf.Reset()
	if err := addAppToml(buf, data, crd.Spec.ChainConfig.App); err != nil {
		return cm, err
	}

	cm.Data = data
	return cm, nil
}

type decodedToml = map[string]any

//go:embed toml/tendermint_default_config.toml
var defaultTendermintToml []byte

func defaultTendermint() decodedToml {
	var data decodedToml
	if err := toml.Unmarshal(defaultTendermintToml, &data); err != nil {
		panic(err)
	}
	return data
}

//go:embed toml/app_default_config.toml
var defaultAppToml []byte

func defaultApp() decodedToml {
	var data decodedToml
	if err := toml.Unmarshal(defaultAppToml, &data); err != nil {
		panic(err)
	}
	return data
}

func addTendermintToml(buf *bytes.Buffer, cmData map[string]string, spec cosmosv1.CosmosChainConfig, cfg ExternalConfig) error {
	base := make(decodedToml)

	if v := spec.LogLevel; v != nil {
		base["log_level"] = v
	}
	if v := spec.LogFormat; v != nil {
		base["log_format"] = v
	}

	tendermint := spec.Tendermint
	p2p := decodedToml{
		"persistent_peers": tendermint.PersistentPeers,
		"seeds":            tendermint.Seeds,
	}
	if v := tendermint.MaxInboundPeers; v != nil {
		p2p["max_num_inbound_peers"] = tendermint.MaxInboundPeers
	}
	if v := tendermint.MaxOutboundPeers; v != nil {
		p2p["max_num_outbound_peers"] = tendermint.MaxOutboundPeers
	}
	if v := cfg.P2PExternalAddress(); v != "" {
		p2p["external_address"] = v
	}

	base["p2p"] = p2p

	if v := tendermint.CorsAllowedOrigins; v != nil {
		base["rpc"] = decodedToml{"cors_allowed_origins": v}
	}

	dst := defaultTendermint()

	mergemap.Merge(dst, base)

	if overrides := tendermint.TomlOverrides; overrides != nil {
		var decoded decodedToml
		_, err := toml.Decode(*overrides, &decoded)
		if err != nil {
			return fmt.Errorf("invalid toml in tendermint overrides: %w", err)
		}
		mergemap.Merge(dst, decoded)
	}

	if err := toml.NewEncoder(buf).Encode(dst); err != nil {
		return err
	}
	cmData[configOverlayFile] = buf.String()
	return nil
}

func addAppToml(buf *bytes.Buffer, cmData map[string]string, app cosmosv1.CosmosAppConfig) error {
	base := make(decodedToml)
	base["minimum-gas-prices"] = app.MinGasPrice
	// Note: The name discrepancy "enable" vs. "enabled" is intentional; a known inconsistency within the app.toml.
	base["api"] = decodedToml{"enabled-unsafe-cors": app.APIEnableUnsafeCORS}
	base["grpc-web"] = decodedToml{"enable-unsafe-cors": app.GRPCWebEnableUnsafeCORS}

	if v := app.HaltHeight; v != nil {
		base["halt-height"] = v
	}

	if pruning := app.Pruning; pruning != nil {
		intStr := func(n *uint32) string {
			v := valOrDefault(n, ptr(uint32(0)))
			return strconv.FormatUint(uint64(*v), 10)
		}
		base["pruning"] = pruning.Strategy
		base["pruning-interval"] = intStr(pruning.Interval)
		base["pruning-keep-every"] = intStr(pruning.KeepEvery)
		base["pruning-keep-recent"] = intStr(pruning.KeepRecent)
	}

	dst := defaultApp()
	mergemap.Merge(dst, base)

	if overrides := app.TomlOverrides; overrides != nil {
		var decoded decodedToml
		_, err := toml.Decode(*overrides, &decoded)
		if err != nil {
			return fmt.Errorf("invalid toml in app overrides: %w", err)
		}
		mergemap.Merge(dst, decoded)
	}

	if err := toml.NewEncoder(buf).Encode(dst); err != nil {
		return err
	}
	cmData[appOverlayFile] = buf.String()
	return nil
}