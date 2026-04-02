// Package sliceutils provides utility functions for slices.
package sliceutils

import "slices"

// Difference returns a slice with the elements that are in a but not in b.
func Difference[T comparable](a, b []T) []T {
	setB := make(map[T]struct{}, len(b))
	for _, item := range b {
		setB[item] = struct{}{}
	}

	var diff []T
	for _, item := range a {
		if _, found := setB[item]; !found {
			diff = append(diff, item)
		}
	}
	return diff
}

// DifferenceFunc returns a slice with the elements that are in a but not in b,
// supporting a function to compare the items.
func DifferenceFunc[S ~[]E, E any](a, b S, f func(E, E) bool) S {
	var diff S
	for _, aItem := range a {
		if !slices.ContainsFunc(b, func(bItem E) bool { return f(aItem, bItem) }) {
			diff = append(diff, aItem)
		}
	}
	return diff
}

// Intersection returns a slice with the elements that are in both a and b.
func Intersection[T comparable](a, b []T) []T {
	setB := make(map[T]struct{}, len(b))
	for _, item := range b {
		setB[item] = struct{}{}
	}

	var intersection []T
	for _, item := range a {
		if _, found := setB[item]; found {
			intersection = append(intersection, item)
		}
	}
	return intersection
}

// Map maps the slice to another slice of the same size, using the provided function.
func Map[T any, S ~[]E, E any](a S, f func(E) T) []T {
	if a == nil {
		return nil
	}

	mapped := make([]T, 0, len(a))
	for _, v := range a {
		mapped = append(mapped, f(v))
	}
	return mapped
}

// EqualContent compares two slices, ensuring that their content is equal.
func EqualContent[S ~[]E, E comparable](a S, b S) bool {
	if len(a) != len(b) {
		return false
	}

	for _, av := range a {
		if !slices.Contains(b, av) {
			return false
		}
	}
	return true
}

// EqualContentFunc compares two slices, ensuring that their content is equal
// using the provided function to compare.
func EqualContentFunc[S ~[]E, E any](a S, b S, f func(E, E) bool) bool {
	if len(a) != len(b) {
		return false
	}

	for _, av := range a {
		if !slices.ContainsFunc(b, func(bv E) bool { return f(av, bv) }) {
			return false
		}
	}
	return true
}
