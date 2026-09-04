package users

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestTemporaryUserAliases(t *testing.T) {
	t.Parallel()

	expired := make(chan temporaryUserAlias, 1)
	aliases := newTemporaryUserAliases(func(alias temporaryUserAlias) bool {
		expired <- alias
		return true
	})
	t.Cleanup(aliases.stop)
	require.NoError(t, aliases.add("first", "user", 1234, "broker", "provider", "user@example.com"))
	retained, err := aliases.retain("second", "user")
	require.NoError(t, err)
	require.True(t, retained)

	require.True(t, aliases.complete("first"))

	aliases.mu.Lock()
	firstKey := temporaryUserAliasKey("first", "user")
	first := aliases.leases[firstKey]
	first.expiresAt = time.Now().Add(-time.Second)
	aliases.leases[firstKey] = first
	aliases.mu.Unlock()
	aliases.expire(firstKey, first.expiresAt)

	alias, ok := aliases.lookup("user")
	require.True(t, ok, "the second lease should keep the alias alive")
	require.Equal(t, uint32(1234), alias.uid)

	require.True(t, aliases.complete("second"))
	aliases.mu.Lock()
	secondKey := temporaryUserAliasKey("second", "user")
	second := aliases.leases[secondKey]
	second.expiresAt = time.Now().Add(-time.Second)
	aliases.leases[secondKey] = second
	aliases.mu.Unlock()
	aliases.expire(secondKey, second.expiresAt)

	_, ok = aliases.lookup("user")
	require.False(t, ok)
	expiredAlias := <-expired
	require.Equal(t, "user", expiredAlias.name)
}

func TestTemporaryUserAliasesRetryFailedCleanup(t *testing.T) {
	t.Parallel()

	aliases := newTemporaryUserAliases(func(temporaryUserAlias) bool {
		return false
	})
	t.Cleanup(aliases.stop)
	require.NoError(t, aliases.add("lease", "user", 1234, "broker", "provider", "user@example.com"))

	key := temporaryUserAliasKey("lease", "user")
	aliases.mu.Lock()
	alias := aliases.leases[key]
	alias.expiresAt = time.Now().Add(-time.Second)
	aliases.leases[key] = alias
	aliases.mu.Unlock()
	aliases.expire(key, alias.expiresAt)

	_, ok := aliases.lookup("user")
	require.True(t, ok, "a failed local-group cleanup should retain the alias for retry")
}

func TestTemporaryUserAliasesLookupAndRetain(t *testing.T) {
	t.Parallel()

	aliases := newTemporaryUserAliases(func(temporaryUserAlias) bool { return true })
	t.Cleanup(aliases.stop)

	require.NoError(t, aliases.add("lease1", "user1", 1001, "broker", "prov", "user1@example.com"))

	// Non-existent user
	_, ok, err := aliases.lookupAndRetain("lease2", "nonexistent")
	require.NoError(t, err)
	require.False(t, ok)

	// Existing lease with exact leaseID and name
	alias, ok, err := aliases.lookupAndRetain("lease1", "user1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "user1", alias.name)
	require.Equal(t, uint32(1001), alias.uid)

	// Existing alias under a different leaseID creates a new lease
	alias2, ok, err := aliases.lookupAndRetain("lease2", "user1")
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "user1", alias2.name)
	require.Equal(t, "lease2", alias2.leaseID)

	// Cleaning in progress
	aliases.mu.Lock()
	aliases.cleaning["user1"] = true
	aliases.mu.Unlock()

	_, _, err = aliases.lookupAndRetain("lease3", "user1")
	require.ErrorContains(t, err, "cleanup is in progress")

	aliases.mu.Lock()
	delete(aliases.cleaning, "user1")
	aliases.mu.Unlock()

	// Fill to max capacity
	aliases.mu.Lock()
	for i := 0; i < maxTemporaryUserAliases; i++ {
		k := fmt.Sprintf("fill-%d", i)
		aliases.leases[k] = temporaryUserAlias{name: k}
	}
	aliases.mu.Unlock()

	_, _, err = aliases.lookupAndRetain("newlease", "user1")
	require.ErrorContains(t, err, "maximum number of temporary user aliases reached")

	// Stopped
	aliases.stop()
	_, _, err = aliases.lookupAndRetain("lease1", "user1")
	require.ErrorContains(t, err, "temporary user aliases have stopped")
}

func TestTemporaryUserAliasesRetain(t *testing.T) {
	t.Parallel()

	aliases := newTemporaryUserAliases(func(temporaryUserAlias) bool { return true })
	t.Cleanup(aliases.stop)

	require.NoError(t, aliases.add("lease1", "user1", 1001, "broker", "prov", "user1@example.com"))

	// Non-existent
	ok, err := aliases.retain("lease2", "nonexistent")
	require.NoError(t, err)
	require.False(t, ok)

	// Existing exact key
	ok, err = aliases.retain("lease1", "user1")
	require.NoError(t, err)
	require.True(t, ok)

	// Cleaning in progress
	aliases.mu.Lock()
	aliases.cleaning["user1"] = true
	aliases.mu.Unlock()

	_, err = aliases.retain("lease2", "user1")
	require.ErrorContains(t, err, "cleanup is in progress")

	aliases.mu.Lock()
	delete(aliases.cleaning, "user1")
	// Fill to max capacity
	for i := 0; i < maxTemporaryUserAliases; i++ {
		k := fmt.Sprintf("fill-%d", i)
		aliases.leases[k] = temporaryUserAlias{name: k}
	}
	aliases.mu.Unlock()

	_, err = aliases.retain("newlease", "user1")
	require.ErrorContains(t, err, "maximum number of temporary user aliases reached")

	// Stopped
	aliases.stop()
	_, err = aliases.retain("lease1", "user1")
	require.ErrorContains(t, err, "temporary user aliases have stopped")
}

func TestTemporaryUserAliasesAdd(t *testing.T) {
	t.Parallel()

	aliases := newTemporaryUserAliases(func(temporaryUserAlias) bool { return true })
	t.Cleanup(aliases.stop)

	// Cleaning in progress
	aliases.mu.Lock()
	aliases.cleaning["user1"] = true
	aliases.mu.Unlock()

	err := aliases.add("lease1", "user1", 1001, "broker", "prov", "user1@example.com")
	require.ErrorContains(t, err, "cleanup is in progress")

	aliases.mu.Lock()
	delete(aliases.cleaning, "user1")
	// Fill to max capacity
	for i := 0; i < maxTemporaryUserAliases; i++ {
		k := fmt.Sprintf("fill-%d", i)
		key := temporaryUserAliasKey(k, k)
		aliases.leases[key] = temporaryUserAlias{leaseID: k, name: k}
	}
	aliases.mu.Unlock()

	err = aliases.add("newlease", "user1", 1001, "broker", "prov", "user1@example.com")
	require.ErrorContains(t, err, "maximum number of temporary user aliases reached")

	// Updating an existing lease at max capacity succeeds
	err = aliases.add("fill-0", "fill-0", 1001, "broker", "prov", "user1@example.com")
	require.NoError(t, err)

	// Stopped
	aliases.stop()
	err = aliases.add("lease1", "user1", 1001, "broker", "prov", "user1@example.com")
	require.ErrorContains(t, err, "temporary user aliases have stopped")
}

func TestTemporaryUserAliasesHasAndLookup(t *testing.T) {
	t.Parallel()

	aliases := newTemporaryUserAliases(func(temporaryUserAlias) bool { return true })
	t.Cleanup(aliases.stop)

	require.False(t, aliases.has("lease1"))
	_, ok := aliases.lookup("user1")
	require.False(t, ok)

	require.NoError(t, aliases.add("lease1", "user1", 1001, "broker", "prov", "user1@example.com"))
	require.True(t, aliases.has("lease1"))
	require.False(t, aliases.has("otherlease"))

	alias, ok := aliases.lookup("user1")
	require.True(t, ok)
	require.Equal(t, "user1", alias.name)
}

func TestTemporaryUserAliasesForUIDAndProtectAndDiscard(t *testing.T) {
	t.Parallel()

	aliases := newTemporaryUserAliases(func(temporaryUserAlias) bool { return true })
	t.Cleanup(aliases.stop)

	require.NoError(t, aliases.add("lease1", "alias1", 1001, "broker", "prov", "user1@example.com"))
	require.NoError(t, aliases.add("lease2", "alias2", 1001, "broker", "prov", "user1@example.com"))
	require.NoError(t, aliases.add("lease3", "alias3", 1002, "broker", "prov", "user2@example.com"))

	u1Aliases := aliases.forUID(1001)
	require.Len(t, u1Aliases, 2)
	u2Aliases := aliases.forUID(1002)
	require.Len(t, u2Aliases, 1)

	allAliases := aliases.all()
	require.Len(t, allAliases, 3)

	// Protect name
	require.NoError(t, aliases.protectNameForUID(1001, "alias1"))

	aliases.mu.Lock()
	aliases.cleaning["alias1"] = true
	aliases.mu.Unlock()
	require.ErrorContains(t, aliases.protectNameForUID(1001, "alias1"), "cleanup is in progress")

	aliases.mu.Lock()
	delete(aliases.cleaning, "alias1")
	aliases.mu.Unlock()

	// Discard name
	aliases.discardNameForUID(1001, "alias1")
	_, ok := aliases.lookup("alias1")
	require.False(t, ok)
	_, ok = aliases.lookup("alias2")
	require.True(t, ok)
}

func TestTemporaryUserAliasesRemove(t *testing.T) {
	t.Parallel()

	expiredCount := 0
	aliases := newTemporaryUserAliases(func(temporaryUserAlias) bool {
		expiredCount++
		return true
	})
	t.Cleanup(aliases.stop)

	require.NoError(t, aliases.add("lease1", "shareduser", 1001, "broker", "prov", "user1@example.com"))
	require.NoError(t, aliases.add("lease2", "shareduser", 1001, "broker", "prov", "user1@example.com"))

	// Removing lease1 should not trigger expiration of shareduser because lease2 remains
	aliases.remove("lease1")
	require.Equal(t, 0, expiredCount)
	_, ok := aliases.lookup("shareduser")
	require.True(t, ok)

	// Removing lease2 should trigger expiration
	aliases.remove("lease2")
	require.Equal(t, 1, expiredCount)
	_, ok = aliases.lookup("shareduser")
	require.False(t, ok)

	// Removing when stopped is a no-op
	aliases.stop()
	aliases.remove("lease1")
}

func TestTemporaryUserAliasesComplete(t *testing.T) {
	t.Parallel()

	aliases := newTemporaryUserAliases(func(temporaryUserAlias) bool { return true })
	t.Cleanup(aliases.stop)

	require.False(t, aliases.complete("nonexistent"))

	require.NoError(t, aliases.add("lease1", "user1", 1001, "broker", "prov", "user1@example.com"))
	require.True(t, aliases.complete("lease1"))
}

func TestTemporaryUserAliasesExpireEdgeCases(t *testing.T) {
	t.Parallel()

	aliases := newTemporaryUserAliases(func(temporaryUserAlias) bool { return true })
	t.Cleanup(aliases.stop)

	require.NoError(t, aliases.add("lease1", "user1", 1001, "broker", "prov", "user1@example.com"))

	key := temporaryUserAliasKey("lease1", "user1")

	// Mismatched expiresAt does nothing
	aliases.expire(key, time.Now().Add(10*time.Hour))
	_, ok := aliases.lookup("user1")
	require.True(t, ok)

	// Future expiresAt does nothing
	aliases.mu.Lock()
	alias := aliases.leases[key]
	aliases.mu.Unlock()
	aliases.expire(key, alias.expiresAt)
	_, ok = aliases.lookup("user1")
	require.True(t, ok)

	// Non-existent key does nothing
	aliases.expire("nonexistent", time.Now().Add(-time.Hour))

	// Expire with another lease for same name only removes key
	require.NoError(t, aliases.add("lease2", "user1", 1001, "broker", "prov", "user1@example.com"))
	aliases.mu.Lock()
	alias = aliases.leases[key]
	alias.expiresAt = time.Now().Add(-time.Hour)
	aliases.leases[key] = alias
	aliases.mu.Unlock()
	aliases.expire(key, alias.expiresAt)
	// lease2 should still keep user1 alive
	_, ok = aliases.lookup("user1")
	require.True(t, ok)
}

func TestTemporaryUserAliasesStop(t *testing.T) {
	t.Parallel()

	var expired []string
	aliases := newTemporaryUserAliases(func(alias temporaryUserAlias) bool {
		expired = append(expired, alias.name)
		return true
	})

	require.NoError(t, aliases.add("lease1", "user1", 1001, "broker", "prov", "user1@example.com"))
	require.NoError(t, aliases.add("lease2", "user2", 1002, "broker", "prov", "user2@example.com"))

	aliases.stop()
	require.ElementsMatch(t, []string{"user1", "user2"}, expired)
}
