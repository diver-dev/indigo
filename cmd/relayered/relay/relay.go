package relay

import (
	"log/slog"
	"sync"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/cmd/relayered/relay/models"
	"github.com/bluesky-social/indigo/cmd/relayered/stream/eventmgr"
	"github.com/bluesky-social/indigo/xrpc"

	"github.com/hashicorp/golang-lru/v2"
	"go.opentelemetry.io/otel"
	"gorm.io/gorm"
)

var tracer = otel.Tracer("relay")

type Relay struct {
	db          *gorm.DB
	dir         identity.Directory
	Logger      *slog.Logger
	Slurper     *Slurper
	Events      *eventmgr.EventManager
	Validator   *Validator
	HostChecker HostChecker
	Config      RelayConfig

	// extUserLk serializes a section of syncHostAccount()
	// TODO: at some point we will want to lock specific DIDs, this lock as is
	// is overly broad, but i dont expect it to be a bottleneck for now
	extUserLk sync.Mutex

	// Management of Socket Consumers
	consumersLk    sync.RWMutex
	nextConsumerID uint64
	consumers      map[uint64]*SocketConsumer

	// Account cache
	accountCache *lru.Cache[string, *models.Account]
}

type RelayConfig struct {
	SSL                     bool
	DefaultRepoLimit        int64
	ConcurrencyPerHost      int64
	MaxQueuePerHost         int64
	ApplyHostClientSettings func(c *xrpc.Client)
	SkipAccountHostCheck    bool // XXX: only used for testing
}

func DefaultRelayConfig() *RelayConfig {
	return &RelayConfig{
		SSL:                true,
		DefaultRepoLimit:   100,
		ConcurrencyPerHost: 100,
		MaxQueuePerHost:    1_000,
	}
}

func NewRelay(db *gorm.DB, vldtr *Validator, evtman *eventmgr.EventManager, dir identity.Directory, config *RelayConfig) (*Relay, error) {

	if config == nil {
		config = DefaultRelayConfig()
	}

	uc, _ := lru.New[string, *models.Account](2_000_000)

	hc := NewHostClient("relayered") // TODO: pass-through a user-agent from config?

	r := &Relay{
		db:          db,
		dir:         dir,
		Logger:      slog.Default().With("system", "relay"),
		Events:      evtman,
		Validator:   vldtr,
		HostChecker: hc,
		Config:      *config,

		consumersLk: sync.RWMutex{},
		consumers:   make(map[uint64]*SocketConsumer),

		accountCache: uc,
	}

	if err := r.MigrateDatabase(); err != nil {
		return nil, err
	}

	slOpts := DefaultSlurperConfig()
	slOpts.SSL = config.SSL
	slOpts.DefaultRepoLimit = config.DefaultRepoLimit
	slOpts.ConcurrencyPerHost = config.ConcurrencyPerHost
	slOpts.MaxQueuePerHost = config.MaxQueuePerHost
	s, err := NewSlurper(db, r.handleFedEvent, slOpts, r.Logger)
	if err != nil {
		return nil, err
	}
	r.Slurper = s

	if err := r.Slurper.RestartAll(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *Relay) MigrateDatabase() error {
	if err := r.db.AutoMigrate(models.DomainBan{}); err != nil {
		return err
	}
	if err := r.db.AutoMigrate(models.Host{}); err != nil {
		return err
	}
	if err := r.db.AutoMigrate(models.Account{}); err != nil {
		return err
	}
	if err := r.db.AutoMigrate(models.AccountRepo{}); err != nil {
		return err
	}
	return nil
}

// simple check of connection to database
func (r *Relay) Healthcheck() error {
	return r.db.Exec("SELECT 1").Error
}
