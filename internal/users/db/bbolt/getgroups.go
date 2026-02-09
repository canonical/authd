package bbolt

import (
	"encoding/json"
	"fmt"

	"go.etcd.io/bbolt"
)

type groupDB struct {
	Name string
	GID  uint32
	UGID string
}

// NewGroupDB creates a new GroupDB.
func NewGroupDB(name string, gid uint32, ugid string, members []string) GroupDB {
	return GroupDB{
		Name:  name,
		GID:   gid,
		UGID:  ugid,
		Users: members,
	}
}

// UserGroups returns all groups for a given user or an error if the database is corrupted or no entry was found.
func (c *Database) UserGroups(uid uint32) ([]GroupDB, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var groups []GroupDB
	err := c.db.View(func(tx *bbolt.Tx) error {
		buckets, err := getAllBuckets(tx)
		if err != nil {
			return err
		}

		// Get group ids for the user.
		groupsForUser, err := getFromBucket[userToGroupsDB](buckets[userToGroupsBucketName], uid)
		if err != nil {
			return err
		}

		for _, gid := range groupsForUser.GIDs {
			// we should always get an entry
			g, err := getFromBucket[groupDB](buckets[groupByIDBucketName], gid)
			if err != nil {
				return err
			}

			// Get user names in the group.
			users, err := getUsersInGroup(buckets, gid)
			if err != nil {
				return err
			}

			groups = append(groups, NewGroupDB(g.Name, g.GID, g.UGID, users))
		}
		return nil
	})

	if err != nil {
		return nil, err
	}

	return groups, nil
}

// UserLocalGroups returns all local groups for a given user or an error if the database is corrupted or no entry was found.
func (c *Database) UserLocalGroups(uid uint32) ([]string, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var localGroups []string
	err := c.db.View(func(tx *bbolt.Tx) error {
		buckets, err := getAllBuckets(tx)
		if err != nil {
			return err
		}

		localGroups, err = getFromBucket[[]string](buckets[userToLocalGroupsBucketName], uid)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	return localGroups, nil
}

// AllGroups returns all groups or an error if the database is corrupted.
func (c *Database) AllGroups() (all []GroupDB, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	err = c.db.View(func(tx *bbolt.Tx) error {
		buckets, err := getAllBuckets(tx)
		if err != nil {
			return err
		}

		return buckets[groupByIDBucketName].ForEach(func(key, value []byte) error {
			var g groupDB
			if err := json.Unmarshal(value, &g); err != nil {
				return fmt.Errorf("can't unmarshal user in bucket %q for key %v: %v", userByIDBucketName, key, err)
			}

			// Get user names in the group.
			users, err := getUsersInGroup(buckets, g.GID)
			if err != nil {
				return err
			}

			all = append(all, NewGroupDB(g.Name, g.GID, g.UGID, users))
			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	return all, nil
}

// usersInGroup returns all user names in a given group. It returns an error if the database is corrupted.
func getUsersInGroup(buckets map[string]bucketWithName, gid uint32) (users []string, err error) {
	usersInGroup, err := getFromBucket[groupToUsersDB](buckets[groupToUsersBucketName], gid)
	if err != nil {
		return nil, err
	}

	for _, uid := range usersInGroup.UIDs {
		// we should always get an entry
		u, err := getFromBucket[UserDB](buckets[userByIDBucketName], uid)
		if err != nil {
			return nil, err
		}
		users = append(users, u.Name)
	}
	return users, nil
}
