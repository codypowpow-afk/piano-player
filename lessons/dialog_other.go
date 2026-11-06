//go:build !windows

package main

func openFileDialog() ([]string, bool) { return nil, false }

const hasDialog = false
