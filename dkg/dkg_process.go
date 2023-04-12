package dkg

import (
	"sync"
	"time"

	"github.com/drand/drand/net"

	"github.com/drand/drand/key"
	"github.com/drand/drand/log"
	"github.com/drand/drand/protobuf/drand"
)

//nolint:revive
type DKGProcess struct {
	lock             sync.Mutex
	store            Store
	network          Network
	internalClient   net.DKGClient
	beaconIdentifier BeaconIdentifier
	log              log.Logger
	config           Config
	// this is public in order to replace it in the test code to simulate failures
	Executions    map[string]Broadcast
	completedDKGs chan<- SharingOutput
}

type Config struct {
	// the length of time after which this node will abort a DKG
	Timeout time.Duration

	// the length of time the phaser should use when moving between DKG phases
	TimeBetweenDKGPhases time.Duration

	// the length of time a node should wait before broadcasting DKG packets in the execution phase
	// to allow other nodes to set up their echo broadcast to prevent race conditions
	KickoffGracePeriod time.Duration

	// whether or not to skip verifying the cryptographic material in the DKG... almost certainly should be false
	SkipKeyVerification bool
}

type ExecutionOutput struct {
	FinalGroup *key.Group
	KeyShare   *key.Share
}

type SharingOutput struct {
	BeaconID string
	Old      *DBState
	New      DBState
}

type Store interface {
	// GetCurrent returns the current DKG information, finished DKG information or fresh DKG information,
	// depending on the state of the world
	GetCurrent(beaconID string) (*DBState, error)

	// GetFinished returns the last completed DKG state (i.e. completed or aborted), or nil if one has not been finished
	GetFinished(beaconID string) (*DBState, error)

	// SaveCurrent stores a DKG packet for an ongoing DKG
	SaveCurrent(beaconID string, state *DBState) error

	// SaveFinished stores a completed, successful DKG and overwrites the current packet
	SaveFinished(beaconID string, state *DBState) error

	// Close closes and cleans up any database handles
	Close() error

	// MigrateFromGroupfile takes an existing groupfile and keyshare, and creates a first epoch DKG state for them.
	// It will fail if DKG state already exists for the given beaconID
	// Deprecated: will only exist in 2.0.0 for migration from v1.5.* to 2.0.0
	MigrateFromGroupfile(beaconID string, groupFile *key.Group, share *key.Share) error
}

type Network interface {
	Send(
		from *drand.Participant,
		to []*drand.Participant,
		action func(client net.DKGClient, peer net.Peer) (*drand.EmptyResponse, error),
	) error
	SendIgnoringConnectionError(
		from *drand.Participant,
		to []*drand.Participant,
		action func(client net.DKGClient, peer net.Peer) (*drand.EmptyResponse, error),
	) error
}

// BeaconIdentifier is necessary because we need to get our identity on a per-beacon basis from the `DrandDaemon`
// but that would introduce a circular dependency
type BeaconIdentifier interface {
	KeypairFor(beaconID string) (*key.Pair, error)
}

func NewDKGProcess(
	store Store,
	beaconIdentifier BeaconIdentifier,
	completedDKGs chan<- SharingOutput,
	privateGateway *net.PrivateGateway,
	config Config,
	l log.Logger,
) *DKGProcess {
	return &DKGProcess{
		store: store,
		network: &GrpcNetwork{
			dkgClient: privateGateway.DKGClient,
			log:       l,
		},
		beaconIdentifier: beaconIdentifier,
		internalClient:   privateGateway.DKGClient,
		log:              l,
		Executions:       make(map[string]Broadcast),
		config:           config,
		completedDKGs:    completedDKGs,
	}
}

func (d *DKGProcess) Close() {
	d.lock.Lock()
	defer d.lock.Unlock()
	for _, e := range d.Executions {
		e.Stop()
	}
	err := d.store.Close()
	if err != nil {
		d.log.Errorw("error closing the database", "err", err)
	}
}

// Migrate takes an existing groupfile and keyshare, and creates a first epoch DKG state for them.
// It will fail if DKG state already exists for the given beaconID
// Deprecated: will only exist in 2.0.0 for migration from v1.5.* to 2.0.0
func (d *DKGProcess) Migrate(beaconID string, groupfile *key.Group, share *key.Share) error {
	return d.store.MigrateFromGroupfile(beaconID, groupfile, share)
}