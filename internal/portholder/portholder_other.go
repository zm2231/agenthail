//go:build !linux && !darwin

package portholder

func Find(int) ([]Holder, error) { return nil, nil }
