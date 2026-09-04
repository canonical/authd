package users

import (
	"errors"
	"slices"
	"sync"
	"time"
)

// Temporary user aliases keep the name a user was known by resolvable while PAM is still using
// it.
//
// A granted authentication can rename the user: enabling use_short_usernames shortens the stored
// name, and disabling it again renames back to the fully qualified one. The rename is applied
// right after the broker grants access, but PAM keeps referring to the old name until the account
// stage is over: pam_unix re-resolves PAM_USER through NSS during pam_acct_mgmt, and a name that
// stopped resolving fails the login with PAM_USER_UNKNOWN although the broker granted access. The
// login that renames the user back broke its own account checks this way.
//
// To prevent that, an update that may rename the user registers the names it invalidates as
// aliases leased to the PAM transaction before the update is applied. While a lease is held, the
// alias resolves to the user through NSS and carries the user's current local-group memberships.
// The PAM account hook releases its lease once the earlier account modules ran, and the alias
// expires after a short grace period. Leases also expire on their own, so a crashed client cannot
// pin a name forever, and a name held by an alias cannot be claimed by another user meanwhile.
const (
	maxTemporaryUserAliases = 10000
	temporaryUserAliasTTL   = 30 * time.Minute
	temporaryUserAliasGrace = 10 * time.Second
	temporaryAliasRetry     = time.Minute
)

type temporaryUserAlias struct {
	leaseID      string
	name         string
	uid          uint32
	brokerID     string
	providerID   string
	fullUsername string
	expiresAt    time.Time
}

type temporaryUserAliases struct {
	mu       sync.Mutex
	callback sync.WaitGroup
	leases   map[string]temporaryUserAlias
	timers   map[string]*time.Timer
	cleaning map[string]bool
	stopped  bool
	onExpire func(temporaryUserAlias) bool
}

func newTemporaryUserAliases(onExpire func(temporaryUserAlias) bool) *temporaryUserAliases {
	return &temporaryUserAliases{
		leases:   make(map[string]temporaryUserAlias),
		timers:   make(map[string]*time.Timer),
		cleaning: make(map[string]bool),
		onExpire: onExpire,
	}
}

func (a *temporaryUserAliases) add(leaseID, name string, uid uint32, brokerID, providerID, fullUsername string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stopped {
		return errors.New("temporary user aliases have stopped")
	}
	if a.cleaning[name] {
		return errors.New("temporary user alias cleanup is in progress")
	}
	key := temporaryUserAliasKey(leaseID, name)
	if _, exists := a.leases[key]; !exists && len(a.leases) >= maxTemporaryUserAliases {
		return errors.New("maximum number of temporary user aliases reached")
	}

	alias := temporaryUserAlias{
		leaseID:      leaseID,
		name:         name,
		uid:          uid,
		brokerID:     brokerID,
		providerID:   providerID,
		fullUsername: fullUsername,
		expiresAt:    time.Now().Add(temporaryUserAliasTTL),
	}
	a.leases[key] = alias
	a.scheduleExpiryLocked(key, alias.expiresAt)
	return nil
}

func (a *temporaryUserAliases) retain(leaseID, name string) (bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stopped {
		return false, errors.New("temporary user aliases have stopped")
	}
	if a.cleaning[name] {
		return false, errors.New("temporary user alias cleanup is in progress")
	}
	key := temporaryUserAliasKey(leaseID, name)
	if existing, ok := a.leases[key]; ok {
		existing.expiresAt = time.Now().Add(temporaryUserAliasTTL)
		a.leases[key] = existing
		a.scheduleExpiryLocked(key, existing.expiresAt)
		return true, nil
	}
	if len(a.leases) >= maxTemporaryUserAliases {
		return false, errors.New("maximum number of temporary user aliases reached")
	}

	for _, alias := range a.leases {
		if alias.name != name {
			continue
		}
		alias.leaseID = leaseID
		alias.expiresAt = time.Now().Add(temporaryUserAliasTTL)
		a.leases[key] = alias
		a.scheduleExpiryLocked(key, alias.expiresAt)
		return true, nil
	}
	return false, nil
}

func (a *temporaryUserAliases) lookupAndRetain(leaseID, name string) (temporaryUserAlias, bool, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.stopped {
		return temporaryUserAlias{}, false, errors.New("temporary user aliases have stopped")
	}
	if a.cleaning[name] {
		return temporaryUserAlias{}, false, errors.New("temporary user alias cleanup is in progress")
	}

	key := temporaryUserAliasKey(leaseID, name)
	if existing, ok := a.leases[key]; ok {
		existing.expiresAt = time.Now().Add(temporaryUserAliasTTL)
		a.leases[key] = existing
		a.scheduleExpiryLocked(key, existing.expiresAt)
		return existing, true, nil
	}
	if len(a.leases) >= maxTemporaryUserAliases {
		return temporaryUserAlias{}, false, errors.New("maximum number of temporary user aliases reached")
	}

	for _, alias := range a.leases {
		if alias.name != name {
			continue
		}
		alias.leaseID = leaseID
		alias.expiresAt = time.Now().Add(temporaryUserAliasTTL)
		a.leases[key] = alias
		a.scheduleExpiryLocked(key, alias.expiresAt)
		return alias, true, nil
	}
	return temporaryUserAlias{}, false, nil
}

func (a *temporaryUserAliases) lookup(name string) (temporaryUserAlias, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, alias := range a.leases {
		if alias.name == name {
			return alias, true
		}
	}
	return temporaryUserAlias{}, false
}

func (a *temporaryUserAliases) has(leaseID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	for _, alias := range a.leases {
		if alias.leaseID == leaseID {
			return true
		}
	}
	return false
}

func (a *temporaryUserAliases) all() []temporaryUserAlias {
	a.mu.Lock()
	defer a.mu.Unlock()

	var aliases []temporaryUserAlias
	for _, alias := range a.leases {
		if !slices.ContainsFunc(aliases, func(existing temporaryUserAlias) bool {
			return existing.name == alias.name && existing.uid == alias.uid
		}) {
			aliases = append(aliases, alias)
		}
	}
	return aliases
}

func (a *temporaryUserAliases) forUID(uid uint32) []temporaryUserAlias {
	return slices.DeleteFunc(a.all(), func(alias temporaryUserAlias) bool {
		return alias.uid != uid
	})
}

func (a *temporaryUserAliases) discardNameForUID(uid uint32, name string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for key, alias := range a.leases {
		if alias.uid == uid && alias.name == name {
			a.removeLocked(key)
		}
	}
}

func (a *temporaryUserAliases) protectNameForUID(uid uint32, name string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cleaning[name] {
		return errors.New("temporary user alias cleanup is in progress")
	}
	expiresAt := time.Now().Add(temporaryUserAliasTTL)
	for key, alias := range a.leases {
		if alias.uid != uid || alias.name != name {
			continue
		}
		alias.expiresAt = expiresAt
		a.leases[key] = alias
		a.scheduleExpiryLocked(key, expiresAt)
	}
	return nil
}

func (a *temporaryUserAliases) remove(leaseID string) {
	a.mu.Lock()
	if a.stopped {
		a.mu.Unlock()
		return
	}

	var expired []temporaryUserAlias
	for key, alias := range a.leases {
		if alias.leaseID != leaseID {
			continue
		}
		if a.hasOtherLeaseForNameLocked(key, alias.name) {
			a.removeLocked(key)
			continue
		}
		a.removeLocked(key)
		a.cleaning[alias.name] = true
		a.callback.Add(1)
		expired = append(expired, alias)
	}
	a.mu.Unlock()

	for _, alias := range expired {
		a.finishExpiry(temporaryUserAliasKey(alias.leaseID, alias.name), alias)
	}
}

func (a *temporaryUserAliases) complete(leaseID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	now := time.Now()
	found := false
	for key, alias := range a.leases {
		if alias.leaseID != leaseID {
			continue
		}
		found = true
		alias.expiresAt = now.Add(temporaryUserAliasGrace)
		a.leases[key] = alias
		a.scheduleExpiryLocked(key, alias.expiresAt)
	}
	return found
}

func temporaryUserAliasKey(leaseID, name string) string {
	return leaseID + "\x00" + name
}

func (a *temporaryUserAliases) expire(key string, expiresAt time.Time) {
	a.mu.Lock()
	alias, ok := a.leases[key]
	if a.stopped || !ok || !alias.expiresAt.Equal(expiresAt) || time.Now().Before(expiresAt) {
		a.mu.Unlock()
		return
	}
	if a.hasOtherLeaseForNameLocked(key, alias.name) {
		a.removeLocked(key)
		a.mu.Unlock()
		return
	}
	a.removeLocked(key)
	a.cleaning[alias.name] = true
	a.callback.Add(1)
	a.mu.Unlock()
	a.finishExpiry(key, alias)
}

func (a *temporaryUserAliases) scheduleExpiryLocked(key string, expiresAt time.Time) {
	if timer := a.timers[key]; timer != nil {
		timer.Stop()
	}
	a.timers[key] = time.AfterFunc(time.Until(expiresAt), func() {
		a.expire(key, expiresAt)
	})
}

func (a *temporaryUserAliases) removeLocked(key string) {
	_, ok := a.leases[key]
	if !ok {
		return
	}
	delete(a.leases, key)
	if timer := a.timers[key]; timer != nil {
		timer.Stop()
		delete(a.timers, key)
	}
}

func (a *temporaryUserAliases) hasOtherLeaseForNameLocked(key, name string) bool {
	for otherKey, other := range a.leases {
		if otherKey != key && other.name == name {
			return true
		}
	}
	return false
}

func (a *temporaryUserAliases) expireAlias(alias temporaryUserAlias) bool {
	return a.onExpire == nil || a.onExpire(alias)
}

func (a *temporaryUserAliases) finishExpiry(key string, alias temporaryUserAlias) {
	defer a.callback.Done()
	cleaned := a.expireAlias(alias)

	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.cleaning, alias.name)
	if cleaned || a.stopped {
		return
	}
	if _, exists := a.leases[key]; exists {
		return
	}
	alias.expiresAt = time.Now().Add(temporaryAliasRetry)
	a.leases[key] = alias
	a.scheduleExpiryLocked(key, alias.expiresAt)
}

func (a *temporaryUserAliases) stop() {
	a.mu.Lock()
	a.stopped = true
	aliasesByName := make(map[string]temporaryUserAlias)
	for leaseID, alias := range a.leases {
		if timer := a.timers[leaseID]; timer != nil {
			timer.Stop()
		}
		aliasesByName[alias.name] = alias
	}
	clear(a.leases)
	clear(a.timers)
	clear(a.cleaning)
	a.mu.Unlock()

	a.callback.Wait()
	for _, alias := range aliasesByName {
		a.expireAlias(alias)
	}
}
