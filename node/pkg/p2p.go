package pkg

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/blocklessnetwork/b7s/config"
	"github.com/blocklessnetwork/b7s/fstore"
	"github.com/blocklessnetwork/b7s/host"
	"github.com/blocklessnetwork/b7s/models/blockless"
	"github.com/blocklessnetwork/b7s/node"
	"github.com/blocklessnetwork/b7s/peerstore"
	"github.com/blocklessnetwork/b7s/store"
	"github.com/cockroachdb/pebble"
	"github.com/multiformats/go-multiaddr"
	"github.com/rs/zerolog"
)

const (
	success = 0
	failure = 1
)

type PebbleNoopLogger struct{}

func (p *PebbleNoopLogger) Infof(_ string, _ ...any)  {}
func (p *PebbleNoopLogger) Fatalf(_ string, _ ...any) {}

// // func main() {
// // 	os.Exit(run())
// // }

func RunP2P(ctx context.Context, log *zerolog.Logger, cfg config.Config, done chan struct{}, failed chan struct{}, pdb *pebble.DB, fdb *pebble.DB) int {
	// Determine node role
	role := func() blockless.NodeRole {
		if cfg.Role == blockless.HeadNodeLabel {
			return blockless.HeadNode
		}
		return blockless.WorkerNode
	}()

	// Convert workspace path to an absolute one.
	workspace, err := filepath.Abs(cfg.Workspace)
	if err != nil {
		log.Error().Err(err).Str("path", cfg.Workspace).Msg("could not determine absolute path for workspace")
		return failure
	}
	cfg.Workspace = workspace

	// Create a new store.
	pstore := store.New(pdb)
	peerstore := peerstore.New(pstore)

	// Get the list of dial back peers.
	peers, err := peerstore.Peers()
	if err != nil {
		log.Error().Err(err).Msg("could not get list of dial-back peers")
		return failure
	}

	// Get the list of boot nodes addresses.
	bootNodeAddrs, err := getBootNodeAddresses(cfg.BootNodes)
	if err != nil {
		log.Error().Err(err).Msg("could not get boot node addresses")
		return failure
	}

	// Create libp2p host.
	log.Info().Str("Addresss", cfg.Connectivity.Address).Uint("Port", cfg.Connectivity.Port).Msg("Creating host")
	host, err := host.New(*log, cfg.Connectivity.Address, cfg.Connectivity.Port,
		host.WithPrivateKey(cfg.Connectivity.PrivateKey),
		host.WithBootNodes(bootNodeAddrs),
		host.WithDialBackPeers(peers),
		host.WithDialBackAddress(cfg.Connectivity.DialbackAddress),
		host.WithDialBackPort(cfg.Connectivity.DialbackPort),
		host.WithDialBackWebsocketPort(cfg.Connectivity.WebsocketDialbackPort),
		host.WithWebsocket(cfg.Connectivity.Websocket),
		host.WithWebsocketPort(cfg.Connectivity.WebsocketPort),
	)
	if err != nil {
		log.Error().Err(err).Str("key", cfg.Connectivity.PrivateKey).Msg("could not create host")
		return failure
	}
	defer host.Close()

	log.Info().
		Str("id", host.ID().String()).
		Strs("addresses", host.Addresses()).
		Int("boot_nodes", len(bootNodeAddrs)).
		Int("dial_back_peers", len(peers)).
		Msg("created host")

	// Set node options.
	opts := []node.Option{
		node.WithRole(role),
		node.WithConcurrency(cfg.Concurrency),
		node.WithAttributeLoading(cfg.LoadAttributes),
	}

	functionStore := store.New(fdb)

	// Create function store.
	fstore := fstore.New(*log, functionStore, cfg.Workspace)

	// Instantiate node.
	node, err := node.New(*log, host, peerstore, fstore, opts...)
	if err != nil {
		log.Error().Err(err).Msg("could not create node")
		return failure
	}

	// Start node main loop in a separate goroutine.
	go func() {

		log.Info().
			Str("role", role.String()).
			Msg("Blockless Node starting")

		err := node.Run(ctx)
		if err != nil {
			log.Error().Err(err).Msg("Blockless Node failed")
			close(failed)
		} else {
			close(done)
		}

		log.Info().Msg("Blockless Node stopped")
	}()

	return success
}

// func needLimiter(cfg *config.Config) bool {
// 	return cfg.CPUPercentage != 1.0 || cfg.MemoryMaxKB > 0
// }

func getBootNodeAddresses(addrs []string) ([]multiaddr.Multiaddr, error) {
	var out []multiaddr.Multiaddr
	for _, addr := range addrs {
		addr, err := multiaddr.NewMultiaddr(addr)
		if err != nil {
			return nil, fmt.Errorf("could not parse multiaddress (addr: %s): %w", addr, err)
		}
		out = append(out, addr)
	}
	return out, nil
}
