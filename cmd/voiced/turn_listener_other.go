//go:build !linux

package main

import (
	"errors"
	"net"
)

func listenTurnSocket(string) (net.Listener, error) {
	return nil, errors.New("Unix peer authentication requires Linux")
}
