package bbolt

import (
	"encoding/json"
	"fmt"

	"go.etcd.io/bbolt"
)

// UserByName returns a user matching this name or an error if the database is corrupted or no entry was found.
func (c *Database) UserByName(name string) (UserDB, error) {
	return getUser(c, userByNameBucketName, name)
}

// AllUsers returns all users or an error if the database is corrupted.
func (c *Database) AllUsers() (all []UserDB, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	err = c.db.View(func(tx *bbolt.Tx) error {
		bucket, err := getBucket(tx, userByIDBucketName)
		if err != nil {
			return err
		}

		return bucket.ForEach(func(key, value []byte) error {
			var u UserDB
			if err := json.Unmarshal(value, &u); err != nil {
				return fmt.Errorf("can't unmarshal user in bucket %q for key %v: %v", userByIDBucketName, key, err)
			}
			all = append(all, u)
			return nil
		})
	})

	if err != nil {
		return nil, err
	}

	return all, nil
}

// getUser returns a user matching the key or an error if the database is corrupted or no entry was found.
func getUser[K uint32 | string](c *Database, bucketName string, key K) (u UserDB, err error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	err = c.db.View(func(tx *bbolt.Tx) error {
		bucket, err := getBucket(tx, bucketName)
		if err != nil {
			return err
		}

		u, err = getFromBucket[UserDB](bucket, key)
		if err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return UserDB{}, err
	}

	return u, nil
}
