package store

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/naveenrajm7/actions-runner-slurm/internal/slurm"
)

const schemaVersion = 1

var (
	bucketMeta   = []byte("meta")
	bucketLeases = []byte("leases")
	keySchema    = []byte("schema-version")
)

type LeaseState string

const (
	StateIntent     LeaseState = "intent"
	StateSubmitting LeaseState = "submitting"
	StateAmbiguous  LeaseState = "ambiguous"
	StatePending    LeaseState = "pending"
	StateStarting   LeaseState = "starting"
	StateRegistered LeaseState = "registered"
	StateIdle       LeaseState = "idle"
	StateBusy       LeaseState = "busy"
	StateCompleting LeaseState = "completing"
	StateTerminal   LeaseState = "terminal"
	StateCleaned    LeaseState = "cleaned"
)

type Lease struct {
	ID             string          `json:"id"`
	ClassName      string          `json:"className"`
	ConfigSnapshot json.RawMessage `json:"configSnapshot"`
	Correlation    string          `json:"correlation"`
	RunnerName     string          `json:"runnerName"`
	RunnerID       int64           `json:"runnerId,omitempty"`
	SlurmJob       slurm.JobID     `json:"slurmJob"`
	State          LeaseState      `json:"state"`
	ClaimHash      string          `json:"claimHash"`
	ClaimExpiresAt time.Time       `json:"claimExpiresAt"`
	JITCiphertext  []byte          `json:"jitCiphertext,omitempty"`
	JITIssuedAt    time.Time       `json:"jitIssuedAt,omitempty"`
	ActionsResult  string          `json:"actionsResult,omitempty"`
	LastSlurmState string          `json:"lastSlurmState,omitempty"`
	PendingReason  string          `json:"pendingReason,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	SubmittedAt    time.Time       `json:"submittedAt,omitempty"`
	StartedAt      time.Time       `json:"startedAt,omitempty"`
	RegisteredAt   time.Time       `json:"registeredAt,omitempty"`
	CompletedAt    time.Time       `json:"completedAt,omitempty"`
}

type Store struct {
	db *bolt.DB
}

func Open(path string) (*Store, error) {
	return open(path, false)
}

func OpenReadOnly(path string) (*Store, error) {
	return open(path, true)
}

func open(path string, readOnly bool) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("state path must be absolute")
	}
	if !readOnly {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, fmt.Errorf("create state directory: %w", err)
		}
	}
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second, NoGrowSync: false, ReadOnly: readOnly})
	if err != nil {
		return nil, fmt.Errorf("open state database (another service may hold the exclusive lock): %w", err)
	}
	s := &Store{db: db}
	if readOnly {
		if err := s.checkSchema(); err != nil {
			_ = db.Close()
			return nil, err
		}
		return s, nil
	}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) checkSchema() error {
	return s.db.View(func(tx *bolt.Tx) error {
		meta := tx.Bucket(bucketMeta)
		leases := tx.Bucket(bucketLeases)
		if meta == nil || leases == nil {
			return errors.New("state database is not initialized")
		}
		current := meta.Get(keySchema)
		if len(current) != 1 || int(current[0]) != schemaVersion {
			return fmt.Errorf("unsupported state schema version %v", current)
		}
		return nil
	})
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	return s.db.Update(func(tx *bolt.Tx) error {
		meta, err := tx.CreateBucketIfNotExists(bucketMeta)
		if err != nil {
			return err
		}
		if _, err := tx.CreateBucketIfNotExists(bucketLeases); err != nil {
			return err
		}
		current := meta.Get(keySchema)
		if len(current) == 0 {
			return meta.Put(keySchema, []byte{schemaVersion})
		}
		if len(current) != 1 || int(current[0]) != schemaVersion {
			return fmt.Errorf("unsupported state schema version %v", current)
		}
		return nil
	})
}

func (s *Store) CreateLease(className string, configSnapshot []byte, claimTTL time.Duration, now time.Time) (Lease, string, error) {
	if className == "" || claimTTL <= 0 {
		return Lease{}, "", errors.New("class name and positive claim TTL are required")
	}
	idBytes := make([]byte, 16)
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(idBytes); err != nil {
		return Lease{}, "", fmt.Errorf("generate lease ID: %w", err)
	}
	if _, err := rand.Read(tokenBytes); err != nil {
		return Lease{}, "", fmt.Errorf("generate claim credential: %w", err)
	}
	id := hex.EncodeToString(idBytes)
	token := hex.EncodeToString(tokenBytes)
	digest := sha256.Sum256([]byte(token))
	now = now.UTC()
	lease := Lease{
		ID: id, ClassName: className, ConfigSnapshot: append([]byte(nil), configSnapshot...),
		Correlation: "slurm-gha/" + id, RunnerName: "slurm-" + id[:12], State: StateIntent,
		ClaimHash: hex.EncodeToString(digest[:]), ClaimExpiresAt: now.Add(claimTTL),
		CreatedAt: now, UpdatedAt: now,
	}
	if err := s.putNew(lease); err != nil {
		return Lease{}, "", err
	}
	return lease, token, nil
}

func (s *Store) Get(id string) (Lease, error) {
	var lease Lease
	err := s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket(bucketLeases).Get([]byte(id))
		if data == nil {
			return os.ErrNotExist
		}
		return json.Unmarshal(data, &lease)
	})
	return lease, err
}

func (s *Store) List() ([]Lease, error) {
	var leases []Lease
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(bucketLeases).ForEach(func(_, value []byte) error {
			var lease Lease
			if err := json.Unmarshal(value, &lease); err != nil {
				return err
			}
			leases = append(leases, lease)
			return nil
		})
	})
	return leases, err
}

func (s *Store) AuthenticateClaim(id, token string, now time.Time) (Lease, error) {
	lease, err := s.Get(id)
	if err != nil {
		return Lease{}, errors.New("invalid claim")
	}
	digest := sha256.Sum256([]byte(token))
	want, err := hex.DecodeString(lease.ClaimHash)
	if err != nil || len(want) != sha256.Size || subtle.ConstantTimeCompare(digest[:], want) != 1 {
		return Lease{}, errors.New("invalid claim")
	}
	if !now.UTC().Before(lease.ClaimExpiresAt) || lease.State == StateTerminal || lease.State == StateCleaned {
		return Lease{}, errors.New("claim expired or revoked")
	}
	return lease, nil
}

func (s *Store) BindJob(id string, job slurm.JobID, now time.Time) error {
	if job.ID <= 0 {
		return errors.New("invalid Slurm job ID")
	}
	return s.update(id, func(lease *Lease) error {
		if lease.SlurmJob.ID != 0 && lease.SlurmJob != job {
			return errors.New("lease is already bound to another Slurm job")
		}
		if lease.State != StateIntent && lease.State != StateSubmitting && lease.State != StateAmbiguous {
			return fmt.Errorf("cannot bind job while lease is %s", lease.State)
		}
		lease.SlurmJob = job
		lease.State = StatePending
		lease.SubmittedAt = now.UTC()
		return nil
	}, now)
}

func (s *Store) Transition(id string, next LeaseState, now time.Time) error {
	return s.update(id, func(lease *Lease) error {
		if !allowedTransition(lease.State, next) {
			return fmt.Errorf("invalid lease transition %s -> %s", lease.State, next)
		}
		lease.State = next
		switch next {
		case StateStarting:
			lease.StartedAt = now.UTC()
		case StateRegistered, StateIdle:
			if lease.RegisteredAt.IsZero() {
				lease.RegisteredAt = now.UTC()
			}
			lease.ClaimHash = ""
			lease.JITCiphertext = nil
		case StateTerminal:
			lease.CompletedAt = now.UTC()
			lease.ClaimHash = ""
			lease.JITCiphertext = nil
		}
		return nil
	}, now)
}

func (s *Store) SaveJITIfAbsent(id string, ciphertext []byte, now time.Time) ([]byte, bool, error) {
	var result []byte
	var stored bool
	err := s.update(id, func(lease *Lease) error {
		if len(lease.JITCiphertext) > 0 {
			result = append([]byte(nil), lease.JITCiphertext...)
			return nil
		}
		lease.JITCiphertext = append([]byte(nil), ciphertext...)
		lease.JITIssuedAt = now.UTC()
		result = append([]byte(nil), ciphertext...)
		stored = true
		return nil
	}, now)
	return result, stored, err
}

func (s *Store) UpdateObservation(id, rawState, pendingReason string, now time.Time) error {
	return s.update(id, func(lease *Lease) error {
		lease.LastSlurmState = rawState
		lease.PendingReason = pendingReason
		return nil
	}, now)
}

func (s *Store) putNew(lease Lease) error {
	data, err := json.Marshal(lease)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketLeases)
		if bucket.Get([]byte(lease.ID)) != nil {
			return errors.New("lease ID collision")
		}
		return bucket.Put([]byte(lease.ID), data)
	})
}

func (s *Store) update(id string, fn func(*Lease) error, now time.Time) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketLeases)
		data := bucket.Get([]byte(id))
		if data == nil {
			return os.ErrNotExist
		}
		var lease Lease
		if err := json.Unmarshal(data, &lease); err != nil {
			return err
		}
		if err := fn(&lease); err != nil {
			return err
		}
		lease.UpdatedAt = now.UTC()
		encoded, err := json.Marshal(lease)
		if err != nil {
			return err
		}
		return bucket.Put([]byte(id), encoded)
	})
}

func allowedTransition(from, to LeaseState) bool {
	if from == to {
		return true
	}
	allowed := map[LeaseState]map[LeaseState]bool{
		StateIntent:     {StateSubmitting: true, StateTerminal: true},
		StateSubmitting: {StatePending: true, StateAmbiguous: true, StateTerminal: true},
		StateAmbiguous:  {StatePending: true, StateTerminal: true},
		StatePending:    {StateStarting: true, StateTerminal: true},
		StateStarting:   {StateRegistered: true, StateIdle: true, StateBusy: true, StateTerminal: true},
		StateRegistered: {StateIdle: true, StateBusy: true, StateCompleting: true, StateTerminal: true},
		StateIdle:       {StateBusy: true, StateCompleting: true, StateTerminal: true},
		StateBusy:       {StateCompleting: true, StateTerminal: true},
		StateCompleting: {StateTerminal: true},
		StateTerminal:   {StateCleaned: true},
	}
	return allowed[from][to]
}

func (s *Store) MarkJobStarted(runnerName string, runnerID int64, now time.Time) (Lease, error) {
	return s.updateByRunnerName(runnerName, func(lease *Lease) error {
		if !allowedTransition(lease.State, StateBusy) {
			return fmt.Errorf("cannot mark %s busy while lease is %s", runnerName, lease.State)
		}
		lease.State = StateBusy
		lease.RunnerID = runnerID
		if lease.RegisteredAt.IsZero() {
			lease.RegisteredAt = now.UTC()
		}
		lease.ClaimHash = ""
		lease.JITCiphertext = nil
		return nil
	}, now)
}

func (s *Store) MarkJobCompleted(runnerName, result string, now time.Time) (Lease, error) {
	return s.updateByRunnerName(runnerName, func(lease *Lease) error {
		if lease.State != StateCompleting && !allowedTransition(lease.State, StateCompleting) {
			return fmt.Errorf("cannot complete %s while lease is %s", runnerName, lease.State)
		}
		lease.State = StateCompleting
		lease.ActionsResult = result
		return nil
	}, now)
}

func (s *Store) updateByRunnerName(runnerName string, fn func(*Lease) error, now time.Time) (Lease, error) {
	var result Lease
	err := s.db.Update(func(tx *bolt.Tx) error {
		bucket := tx.Bucket(bucketLeases)
		var foundKey []byte
		var found Lease
		err := bucket.ForEach(func(key, value []byte) error {
			var lease Lease
			if err := json.Unmarshal(value, &lease); err != nil {
				return err
			}
			if lease.RunnerName != runnerName || lease.State == StateCleaned {
				return nil
			}
			if foundKey != nil {
				return fmt.Errorf("multiple active leases found for runner %q", runnerName)
			}
			foundKey = append([]byte(nil), key...)
			found = lease
			return nil
		})
		if err != nil {
			return err
		}
		if foundKey == nil {
			return os.ErrNotExist
		}
		if err := fn(&found); err != nil {
			return err
		}
		found.UpdatedAt = now.UTC()
		encoded, err := json.Marshal(found)
		if err != nil {
			return err
		}
		if err := bucket.Put(foundKey, encoded); err != nil {
			return err
		}
		result = found
		return nil
	})
	return result, err
}
