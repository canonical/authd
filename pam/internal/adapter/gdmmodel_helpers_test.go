package adapter

import (
	"fmt"

	"golang.org/x/exp/constraints"
)

func isSupersetOf[T constraints.Integer](a []T, b []T) bool {
	tracker := make(map[T]int)
	for _, v := range a {
		tracker[v]++
	}

	for _, value := range b {
		n, found := tracker[value]
		if !found {
			return false
		}
		if n < 1 {
			return false
		}
		tracker[value] = n - 1
	}
	return true
}

func stringifySlice[T any](a []T) []string {
	var s []string
	for _, v := range a {
		s = append(s, fmt.Sprint(v))
	}
	return s
}
