package users

import (
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/canonical/authd/internal/users/localentries"
	"github.com/stretchr/testify/require"
)

func TestGetIDCandidate(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		idMin uint32
		idMax uint32
		used  []uint32

		wantPos int
		wantID  uint32
		wantErr bool
	}{
		"Generated_ID_is_within_the_defined_range": {
			idMin: 1000, idMax: 2000,
			wantID:  1000,
			wantPos: 0,
		},
		"Generate_ID_with_minimum_ID_equal_to_maximum_ID": {
			idMin: 1000, idMax: 1000,
			wantID:  1000,
			wantPos: 0,
		},
		"UsedIDs_outside_range_are_ignored": {
			idMin: 1000, idMax: 2000,
			used:    []uint32{1, 2, 3, 999, 2001, 3000},
			wantID:  1000,
			wantPos: 4,
		},
		"UsedIDs_in_middle_of_range": {
			idMin: 1000, idMax: 1005,
			used:    []uint32{1002, 1003},
			wantID:  1004,
			wantPos: 2,
		},
		"UsedIDs_at_the_end_of_range": {
			idMin: 1000, idMax: 1005,
			used:    []uint32{1002, 1005},
			wantID:  1000,
			wantPos: 0,
		},
		"UsedIDs_minID_equals_maxID_and_unused": {
			idMin: 1000, idMax: 1000,
			wantID:  1000,
			wantPos: 0,
		},
		"UsedIDs_last_value_is_smaller_than_the_minimum_id": {
			idMin: 1000, idMax: 2000,
			used:    []uint32{20, 100},
			wantID:  1000,
			wantPos: 2,
		},
		"Intermediate_value_after_MaxUint32_is_reached": {
			idMin: math.MaxUint32 - 2, idMax: math.MaxUint32,
			used:    []uint32{math.MaxUint32 - 2, math.MaxUint32},
			wantID:  math.MaxUint32 - 1,
			wantPos: 1,
		},

		"Error_if_no_available_ID_in_range": {
			idMin: 1000, idMax: 2000,
			used: func() []uint32 {
				used := make([]uint32, 0, 1001)
				for i := uint32(1000); i <= 2000; i++ {
					used = append(used, i)
				}
				return used
			}(),
			wantErr: true,
		},
		"Error_if_usedIDs_minID_equals_maxID_and_is_used": {
			idMin: 1000, idMax: 1000,
			used:    []uint32{1000},
			wantErr: true,
		},
		"Error_if_minID_greater_than_maxID": {
			idMin: 100000, idMax: 10000,
			wantErr: true,
		},
		"Error_if_all_the_values_next_to_MaxUint32_are_used": {
			idMin: math.MaxUint32 - 2, idMax: math.MaxUint32,
			used:    []uint32{math.MaxUint32 - 2, math.MaxUint32 - 1, math.MaxUint32},
			wantErr: true,
		},
		"Error_if_only_MaxUint32_is_available": {
			idMin:   math.MaxUint32,
			idMax:   math.MaxUint32,
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			id, pos, err := getIDCandidate(tc.idMin, tc.idMax, tc.used)
			if tc.wantErr {
				require.Error(t, err, "getIDCandidate not returned an error as expected")
				require.Equal(t, -1, pos, "getIDCandidate did not return the expected usedIDs position")
				return
			}

			require.NoError(t, err, "getIDCandidate returned an unexpected error")
			require.GreaterOrEqual(t, int(id), int(tc.idMin), "ID is less than idMin")
			require.LessOrEqual(t, int(id), int(tc.idMax), "ID is greater than idMax")
			require.GreaterOrEqual(t, pos, 0, "Position is not greater or equal 0")
			require.Equal(t, int(tc.wantID), int(id), "Generated ID does not match expected value")
			require.Equal(t, tc.wantPos, pos, "getIDCandidate returned unexpected position in usedIDs")
		})
	}
}

type IDOwnerMock struct {
	usedUIDs []uint32
	usedGIDs []uint32
}

func (m IDOwnerMock) UsedUIDs() ([]uint32, error) { return m.usedUIDs, nil }
func (m IDOwnerMock) UsedGIDs() ([]uint32, error) { return m.usedGIDs, nil }

func TestGenerateIDMocked(t *testing.T) {
	t.Parallel()

	allAvailableIDsFunc := func(_ *localentries.UserDBLocked, u uint32) (bool, error) {
		return true, nil
	}
	noAvailableIDFunc := func(_ *localentries.UserDBLocked, u uint32) (bool, error) {
		return false, nil
	}
	noUsedIDFunc := func(_ *localentries.UserDBLocked, o IDOwner) ([]uint32, error) {
		return nil, nil
	}
	getOwnerUsedUIDsFunc := func(_ *localentries.UserDBLocked, o IDOwner) ([]uint32, error) {
		return o.UsedUIDs()
	}
	getOwnerUsedGIDsFunc := func(_ *localentries.UserDBLocked, o IDOwner) ([]uint32, error) {
		return o.UsedGIDs()
	}

	tests := map[string]struct {
		genID          generateID
		owner          IDOwner
		generator      IDGenerator
		noCleanupCheck bool

		wantErr bool
		wantID  uint32
	}{
		"Generated_ID_is_within_the_defined_range": {
			genID: generateID{
				idType: "UID",
				minID:  10000, maxID: 10010,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    noUsedIDFunc,
			},
			wantID: 10000,
		},
		"Generated_ID_when_only_one_possible_value_is_available": {
			genID: generateID{
				idType: "UID",
				minID:  10000, maxID: 10000,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    noUsedIDFunc,
			},
			wantID: 10000,
		},
		"Owner_with_some_used_UIDs": {
			genID: generateID{
				idType: "UID",
				minID:  500, maxID: 505,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    getOwnerUsedUIDsFunc,
			},
			owner:  IDOwnerMock{usedUIDs: []uint32{500, 501, 502}},
			wantID: 503,
		},
		"Owner_with_some_used_GIDs": {
			genID: generateID{
				idType: "GID",
				minID:  200, maxID: 202,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    getOwnerUsedGIDsFunc,
			},
			owner:  IDOwnerMock{usedGIDs: []uint32{200}},
			wantID: 201,
		},
		"Owner_with_no_used_IDs": {
			genID: generateID{
				idType: "UID",
				minID:  300, maxID: 301,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    getOwnerUsedUIDsFunc,
			},
			owner:  IDOwnerMock{usedUIDs: nil},
			wantID: 300,
		},
		"PendingIDs_are_considered": {
			genID: generateID{
				idType: "UID",
				minID:  100, maxID: 105,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    noUsedIDFunc,
			},
			generator:      IDGenerator{pendingIDs: []uint32{100, 101, 102}},
			wantID:         103,
			noCleanupCheck: true,
		},
		"Used_ids_are_always_sorted": {
			genID: generateID{
				idType: "UID",
				minID:  300, maxID: 303,
				isAvailableID: func(_ *localentries.UserDBLocked, u uint32) (bool, error) {
					return u == 302, nil
				},
				getUsedIDs: getOwnerUsedUIDsFunc,
			},
			owner:  IDOwnerMock{usedUIDs: []uint32{300, 303}},
			wantID: 302,
		},
		"Root_uid_is_always_skipped": {
			genID: generateID{
				idType: "UID",
				minID:  0, maxID: 1,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    noUsedIDFunc,
			},
			wantID: 1,
		},
		"Nobody_and_uid-t_16bit_are_always_skipped": {
			genID: generateID{
				idType: "UID",
				minID:  nobodyID, maxID: nobodyID + 2,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    noUsedIDFunc,
			},
			wantID: nobodyID + 2,
		},
		"uid-t_32bit_is_always_skipped": {
			genID: generateID{
				idType: "UID",
				minID:  uidT32MinusOne - 2, maxID: uidT32MinusOne,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    getOwnerUsedUIDsFunc,
			},
			owner:  IDOwnerMock{usedUIDs: []uint32{uidT32MinusOne}},
			wantID: uidT32MinusOne - 2,
		},
		"MaxUint32_is_always_skipped": {
			genID: generateID{
				idType: "UID",
				minID:  math.MaxUint32 - 2, maxID: math.MaxUint32,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    getOwnerUsedUIDsFunc,
			},
			owner:  IDOwnerMock{usedUIDs: []uint32{math.MaxUint32 - 1}},
			wantID: math.MaxUint32 - 2,
		},

		// Error cases
		"Error_if_minID_is_equal_to_maxID": {
			genID: generateID{
				idType: "UID",
				minID:  10001, maxID: 10000,
			},
			wantErr: true,
		},
		"Error_if_ID_not_available_due_to_isAvailableID": {
			genID: generateID{
				idType: "UID",
				minID:  10000, maxID: 10010,
				isAvailableID: noAvailableIDFunc,
				getUsedIDs:    noUsedIDFunc,
			},
			wantErr: true,
		},
		"Error_if_ID_not_available_due_to_isAvailableID_error": {
			genID: generateID{
				idType: "UID",
				minID:  10000, maxID: 10010,
				isAvailableID: func(_ *localentries.UserDBLocked, u uint32) (bool, error) {
					return false, errors.New("test error")
				},
				getUsedIDs: noUsedIDFunc,
			},
			wantErr: true,
		},
		"Error_if_all_IDs_are_used": {
			genID: generateID{
				idType: "UID",
				minID:  10000, maxID: 10002,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs: func(_ *localentries.UserDBLocked, o IDOwner) ([]uint32, error) {
					return []uint32{10000, 10001, 10002}, nil
				},
			},
			wantErr: true,
		},
		"Error_if_all_the_IDs_in_range_are_unavailable": {
			genID: generateID{
				idType: "ID",
				minID:  0, maxID: 100,
				isAvailableID: noAvailableIDFunc,
				getUsedIDs:    noUsedIDFunc,
			},
			wantErr: true,
		},
		"Error_if_all_the_IDs_in_range_are_unavailable_after_max_checks": {
			genID: generateID{
				idType: "ID",
				minID:  10000, maxID: math.MaxUint32,
				isAvailableID: func(_ *localentries.UserDBLocked, u uint32) (bool, error) {
					return u < 10000, nil
				},
				getUsedIDs: noUsedIDFunc,
			},
			wantErr: true,
		},
		"Error_if_getUsedIDs_returns_error": {
			genID: generateID{
				idType: "UID",
				minID:  10000, maxID: 10010,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs: func(_ *localentries.UserDBLocked, o IDOwner) ([]uint32, error) {
					return nil, errors.New("usedIDs error")
				},
			},
			wantErr: true,
		},
		"Owner_with_all_UIDs_used": {
			genID: generateID{
				idType: "UID",
				minID:  10, maxID: 12,
				isAvailableID: allAvailableIDsFunc,
				getUsedIDs:    getOwnerUsedUIDsFunc,
			},
			owner:   IDOwnerMock{usedUIDs: []uint32{10, 11, 12}},
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if tc.genID.idType == "" {
				tc.genID.idType = t.Name()
			} else {
				tc.genID.idType = fmt.Sprintf("%s_%s", t.Name(),
					tc.genID.idType)
			}

			lockedMock := &localentries.UserDBLocked{}
			id, cleanup, err := tc.generator.generateID(lockedMock,
				tc.owner, tc.genID)
			if tc.wantErr {
				require.Error(t, err, "Expected error but got none")
				require.Zero(t, id, "Expected id to be zero")
				return
			}
			require.NoError(t, err, "GenerateID should not fail")

			t.Cleanup(func() {
				cleanup()

				if tc.noCleanupCheck {
					return
				}
				require.Empty(t, tc.generator.pendingIDs, "Expected generator to be empty after cleanup")
			})

			require.GreaterOrEqual(t, int(id), int(tc.genID.minID), "Id %d is less than minID %d",
				id, tc.genID.minID)
			require.LessOrEqual(t, int(id), int(tc.genID.maxID), "Id %d is greater than maxID %d",
				id, tc.genID.maxID)
			require.Equal(t, int(tc.wantID), int(id), "Generated unexpected ID")
			require.Contains(t, tc.generator.pendingIDs, id, "Id %d not found in pendingIDs", id)
		})
	}
}
