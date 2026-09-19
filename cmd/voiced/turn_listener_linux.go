package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

type turnListener struct {
	*net.UnixListener
	path     string
	identity os.FileInfo
	once     sync.Once
}

func checkDirectory(path string, uid uint32, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || info.Mode().Perm() != mode || stat.Uid != uid {
		return fmt.Errorf("untrusted socket directory: %s", path)
	}
	return nil
}

func listenTurnSocket(path string) (net.Listener, error) {
	if err := validateSocketPath(path); err != nil {
		return nil, err
	}
	parent := filepath.Dir(path)
	if err := checkDirectory(parent, uint32(os.Geteuid()), 0700); err != nil {
		return nil, err
	}
	// Every ancestor must be a real directory outside tenant write authority.
	for ancestor := filepath.Dir(parent); ; ancestor = filepath.Dir(ancestor) {
		info, err := os.Lstat(ancestor)
		if err != nil {
			return nil, err
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || !info.IsDir() || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return nil, fmt.Errorf("untrusted socket ancestor: %s", ancestor)
		}
		if ancestor == "/" {
			break
		}
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("turn socket path already exists or cannot be inspected")
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return nil, err
	}
	listener.SetUnlinkOnClose(false)
	identity, err := os.Lstat(path)
	if err != nil {
		listener.Close()
		return nil, err
	}
	owned := &turnListener{UnixListener: listener, path: path, identity: identity}
	if err := os.Chmod(path, 0600); err != nil {
		owned.Close()
		return nil, err
	}
	actual, err := os.Lstat(path)
	if err != nil || !os.SameFile(identity, actual) || actual.Mode()&os.ModeSocket == 0 || actual.Mode().Perm() != 0600 {
		owned.Close()
		return nil, errors.New("turn socket ownership changed during bind")
	}
	stat, ok := actual.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		owned.Close()
		return nil, errors.New("unexpected turn socket owner")
	}
	if err := checkDirectory(parent, uint32(os.Geteuid()), 0700); err != nil {
		owned.Close()
		return nil, err
	}
	return owned, nil
}

func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var credentials *syscall.Ucred
	var credentialErr error
	if err = raw.Control(func(fd uintptr) {
		credentials, credentialErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if credentialErr != nil {
		return 0, credentialErr
	}
	if credentials == nil {
		return 0, errors.New("missing kernel peer credentials")
	}
	return credentials.Uid, nil
}

func (l *turnListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.AcceptUnix()
		if err != nil {
			return nil, err
		}
		uid, err := peerUID(conn)
		if err == nil && uid == 0 {
			return conn, nil
		}
		conn.Close()
		if err != nil {
			return nil, fmt.Errorf("kernel peer credentials unavailable: %w", err)
		}
	}
}

func (l *turnListener) Close() error {
	err := l.UnixListener.Close()
	l.once.Do(func() {
		if actual, statErr := os.Lstat(l.path); statErr == nil && os.SameFile(l.identity, actual) {
			_ = os.Remove(l.path)
		}
	})
	return err
}
