package bbolt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/canonical/authd/log"
	"go.etcd.io/bbolt"
)

// deleteUserFromGroup removes the uid from the group.
// If the group is empty after the uid gets removed, the group is deleted from the database.
func deleteUserFromGroup(buckets map[string]bucketWithName, uid, gid uint32) error {
	log.Debugf(context.TODO(), "Removing user %d from group %d", uid, gid)
	groupToUsers, err := getFromBucket[groupToUsersDB](buckets[groupToUsersBucketName], gid)
	if err != nil && !errors.Is(err, NoDataFoundError{}) {
		return err
	}

	groupToUsers.UIDs = slices.DeleteFunc(groupToUsers.UIDs, func(id uint32) bool { return id == uid })

	// Update the group entry with the new list of UIDs
	updateBucket(buckets[groupToUsersBucketName], gid, groupToUsers)

	return nil
}

// deleteOrphanedUsers removes users from the UserByID bucket that are not in the UserByName bucket.
func deleteOrphanedUsers(db *bbolt.DB) error {
	log.Debug(context.TODO(), "Cleaning up orphaned user records")

	err := db.Update(func(tx *bbolt.Tx) error {
		buckets, err := getAllBuckets(tx)
		if err != nil {
			return err
		}

		return buckets[userByIDBucketName].ForEach(func(k, v []byte) error {
			var user UserDB
			if err := json.Unmarshal(v, &user); err != nil {
				log.Warningf(context.TODO(), "Error loading user record {%s: %s}: %v", k, v, err)
				return nil
			}

			user2, err := getFromBucket[UserDB](buckets[userByNameBucketName], user.Name)
			if err != nil && !errors.Is(err, NoDataFoundError{}) {
				log.Warningf(context.TODO(), "Error loading user record %q: %v", user.Name, err)
				return nil
			}
			if errors.Is(err, NoDataFoundError{}) || user2.UID != user.UID {
				log.Warningf(context.TODO(), "Removing orphaned user record %q with UID %d", user.Name, user.UID)
				return deleteOrphanedUser(buckets, user.UID)
			}

			return nil
		})
	})
	if err != nil {
		return err
	}

	log.Debug(context.TODO(), "Done cleaning up orphaned user records")
	return nil
}

// deleteOrphanedUser removes all user records with the given UID from the buckets, except the UserByName bucket.
func deleteOrphanedUser(buckets map[string]bucketWithName, uid uint32) (err error) {
	uidKey := []byte(strconv.FormatUint(uint64(uid), 10))

	if err := buckets[userByIDBucketName].Delete(uidKey); err != nil {
		return fmt.Errorf("can't delete user with UID %d from userByID bucket: %v", uid, err)
	}

	groups, err := getFromBucket[userToGroupsDB](buckets[userToGroupsBucketName], uid)
	if err != nil && !errors.Is(err, NoDataFoundError{}) {
		return err
	}
	for _, gid := range groups.GIDs {
		if err := deleteUserFromGroup(buckets, uid, gid); err != nil {
			return err
		}
	}

	if err := buckets[userToGroupsBucketName].Delete(uidKey); err != nil {
		return fmt.Errorf("can't delete user with UID %d from userToGroups bucket: %v", uid, err)
	}
	if err := buckets[userToBrokerBucketName].Delete(uidKey); err != nil {
		return fmt.Errorf("can't delete user with UID %d from userToBroker bucket: %v", uid, err)
	}

	return nil
}
