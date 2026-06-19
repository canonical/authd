package users

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sort"

	"github.com/canonical/authd/internal/users/localentries"
	"github.com/canonical/authd/log"
)

// IDGeneratorIface is the interface that must be implemented by the ID generator.
type IDGeneratorIface interface {
	GenerateUID(lockedEntries *localentries.UserDBLocked, owner IDOwner) (uid uint32, cleanup func(), err error)
	GenerateGID(lockedEntries *localentries.UserDBLocked, owner IDOwner) (gid uint32, cleanup func(), err error)
}

// IDOwner is the interface that must be implemented by the IDs owner to provide
// the currently used UIDs and GIDs.
type IDOwner interface {
	UsedUIDs() ([]uint32, error)
	UsedGIDs() ([]uint32, error)
}

// IDGenerator is an ID generator that generates UIDs and GIDs in a specific range.
type IDGenerator struct {
	UIDMin uint32
	UIDMax uint32
	GIDMin uint32
	GIDMax uint32

	// IDs generated but not saved to the database yet.
	// This is used to avoid generating the same ID multiple times.
	// We don't differentiate between UIDs and GIDs here, because:
	// * When picking a UID, we avoid IDs which we already used as GIDs,
	//   because the UID is also used as the GID of the user private group.
	// * When picking a GID, we avoid IDs which we already used as UIDs,
	//   because those are also GIDs of the user private groups.
	pendingIDs []uint32
}

// If no valid ID is found after this many attempts, something is likely wrong.
// A possible cause is another NSS source using the same ID range.
// We want users to report such cases, so we can consider including IDs
// from other NSS sources when determining which candidates to exclude.
const maxIDGenerateIterations = 1000

// Special Linux UIDs, see https://systemd.io/UIDS-GIDS/
const (
	rootID         uint32 = 0
	nobodyID       uint32 = 65534
	uidT32MinusOne uint32 = math.MaxUint32
	uidT16MinusOne uint32 = math.MaxUint16
)

// GenerateUID generates a random UID in the configured range.
func (g *IDGenerator) GenerateUID(lockedEntries *localentries.UserDBLocked, owner IDOwner) (uint32, func(), error) {
	return g.generateID(lockedEntries, owner, generateID{
		idType:        "UID",
		minID:         g.UIDMin,
		maxID:         g.UIDMax,
		getUsedIDs:    g.getUsedIDs,
		isAvailableID: g.isUIDAvailable,
	})
}

// GenerateGID generates a random GID in the configured range.
func (g *IDGenerator) GenerateGID(lockedEntries *localentries.UserDBLocked, owner IDOwner) (uint32, func(), error) {
	return g.generateID(lockedEntries, owner, generateID{
		idType:        "GID",
		minID:         g.GIDMin,
		maxID:         g.GIDMax,
		getUsedIDs:    g.getUsedGIDs,
		isAvailableID: g.isGIDAvailable,
	})
}

// This is an utility struct to allow code sharing simplifying the arguments passing.
type generateID struct {
	idType        string
	minID, maxID  uint32
	isAvailableID func(*localentries.UserDBLocked, uint32) (bool, error)
	getUsedIDs    func(*localentries.UserDBLocked, IDOwner) ([]uint32, error)
}

func (g *IDGenerator) generateID(lockedEntries *localentries.UserDBLocked, owner IDOwner, args generateID) (id uint32, cleanup func(), err error) {
	if args.minID > args.maxID {
		return 0, nil, errors.New("minID must be less than or equal to maxID")
	}

	usedIDs, err := args.getUsedIDs(lockedEntries, owner)
	if err != nil {
		return 0, nil, err
	}

	// Add pending IDs to the used IDs to ensure we don't generate the same ID again
	usedIDs = append(usedIDs, g.pendingIDs...)

	usedIDs = normalizeUsedIDs(usedIDs, args.minID, args.maxID)

	maxAttempts := min(maxIDGenerateIterations, args.maxID-args.minID+1)

	for range maxAttempts {
		id, pos, err := getIDCandidate(args.minID, args.maxID, usedIDs)
		if err != nil {
			return 0, nil, err
		}

		available, err := args.isAvailableID(lockedEntries, id)
		if err != nil {
			return 0, nil, err
		}

		if !available {
			// Keep track of the id, but preserving usedIDs sorted, since we
			// use the binary search to add elements to it.
			usedIDs = slices.Insert(usedIDs, pos, id)

			// If the GID is not available, try the next candidate
			log.Debugf(context.Background(), "%s %d is already used", args.idType, id)
			continue
		}

		g.pendingIDs = append(g.pendingIDs, id)
		cleanup = func() {
			idx := slices.Index(g.pendingIDs, id)
			g.pendingIDs = append(g.pendingIDs[:idx], g.pendingIDs[idx+1:]...)
		}
		return id, cleanup, nil
	}

	return 0, nil, fmt.Errorf("failed to find a valid %s for after %d attempts",
		args.idType, maxAttempts)
}

func getIDCandidate(minID, maxID uint32, usedIDs []uint32) (id uint32, uniqueIDsPos int, err error) {
	// Pick the preferred ID candidate, starting with minID.
	preferredID := minID

	if len(usedIDs) > 0 {
		// If there are used IDs, we prefer the next ID above the highest used.
		// Note that this may overflow, and so go back to 0, but the next check
		// will adjust the value to the minID.
		preferredID = usedIDs[len(usedIDs)-1] + 1

		// Ensure that the preferred ID is not less than the minimum ID.
		preferredID = max(preferredID, minID)
	}

	// Try IDs starting from the preferred ID up to the maximum ID.
	// Overflows are avoided by the "id < math.MaxUint32" condition.
	for id := preferredID; id <= maxID && id < math.MaxUint32; id++ {
		if isReservedID(id) {
			continue
		}

		pos, found := slices.BinarySearch(usedIDs, id)
		if found {
			continue
		}

		return id, pos, nil
	}

	// Fallback: try IDs from the minimum ID up to the preferred ID.
	// Overflows are avoided by "id < preferredID" condition, because
	// preferredID is a uint32, so the condition must be false when
	// id == math.MaxUint32.
	for id := minID; id < preferredID && id <= maxID; id++ {
		if isReservedID(id) {
			continue
		}

		pos, found := slices.BinarySearch(usedIDs, id)
		if found {
			continue
		}

		return id, pos, nil
	}

	return 0, -1, errors.New("no available ID in range")
}

func isReservedID(id uint32) bool {
	switch id {
	case rootID,
		nobodyID,
		uidT32MinusOne,
		uidT16MinusOne:
		return true
	default:
		return false
	}
}

func (g *IDGenerator) isUIDAvailable(lockedEntries *localentries.UserDBLocked, uid uint32) (bool, error) {
	if unique, err := lockedEntries.IsUniqueUID(uid); !unique || err != nil {
		return false, err
	}

	return true, nil
}

func (g *IDGenerator) isGIDAvailable(lockedEntries *localentries.UserDBLocked, gid uint32) (bool, error) {
	if unique, err := lockedEntries.IsUniqueGID(gid); !unique || err != nil {
		return false, err
	}

	return true, nil
}

func (g *IDGenerator) getUsedIDs(lockedEntries *localentries.UserDBLocked, owner IDOwner) ([]uint32, error) {
	usedUIDs, err := g.getUsedUIDs(lockedEntries, owner)
	if err != nil {
		return nil, err
	}

	// For the user ID we also need to exclude all the GIDs, since the user
	// private group ID will match its own uid, so if we don't do this, we may
	// have a clash later on, when trying to add the group for this user.
	usedGIDs, err := g.getUsedGIDs(lockedEntries, owner)
	if err != nil {
		return nil, err
	}

	return append(usedUIDs, usedGIDs...), nil
}

func (g *IDGenerator) getUsedUIDs(lockedEntries *localentries.UserDBLocked, owner IDOwner) ([]uint32, error) {
	// Get the users from the authd database and pre-auth users.
	uids, err := owner.UsedUIDs()
	if err != nil {
		return nil, err
	}

	// Get the user entries from the passwd file. We don't use NSS here,
	// because for picking the next higher ID we only want to consider the users
	// in /etc/passwd and in the authd database, not from other sources like LDAP.
	userEntries, err := lockedEntries.GetLocalUserEntries()
	if err != nil {
		return nil, err
	}
	for _, user := range userEntries {
		uids = append(uids, user.UID)
	}

	return uids, nil
}

func (g *IDGenerator) getUsedGIDs(lockedEntries *localentries.UserDBLocked, owner IDOwner) ([]uint32, error) {
	gids, err := owner.UsedGIDs()
	if err != nil {
		return nil, err
	}

	// Get the group entries from the passwd file. We don't use NSS here,
	// because for picking the next higher ID we only want to consider the groups
	// in /etc/group and the users in /etc/group and in the authd database, not
	// from other sources like LDAP (in case merge method is used).
	groupEntries, err := lockedEntries.GetLocalGroupEntries()
	if err != nil {
		return nil, err
	}
	for _, group := range groupEntries {
		gids = append(gids, group.GID)
	}

	// And include users GIDs too.
	userEntries, err := lockedEntries.GetLocalUserEntries()
	if err != nil {
		return nil, err
	}
	for _, user := range userEntries {
		gids = append(gids, user.GID)
	}

	return gids, nil
}

func normalizeUsedIDs(usedIDs []uint32, minID, maxID uint32) []uint32 {
	// Sort usedIDs so we can binary search
	sort.Slice(usedIDs, func(i, j int) bool { return usedIDs[i] < usedIDs[j] })

	// Cut off usedIDs to the range we care about
	if len(usedIDs) > 0 && usedIDs[0] < minID {
		// Find the first ID >= minID
		firstIndex := slices.IndexFunc(usedIDs, func(id uint32) bool { return id >= minID })
		if firstIndex != -1 {
			// Slice usedIDs to start from the first ID >= minID
			usedIDs = usedIDs[firstIndex:]
		}
	}
	if len(usedIDs) > 0 && usedIDs[len(usedIDs)-1] > maxID {
		// Find the last ID <= maxID
		lastIndex := slices.IndexFunc(usedIDs, func(id uint32) bool { return id > maxID })
		if lastIndex != -1 {
			// Slice usedIDs to end at the last ID <= maxID
			usedIDs = usedIDs[:lastIndex]
		}
	}

	// Remove duplicates from usedIDs
	return slices.Compact(usedIDs)
}
