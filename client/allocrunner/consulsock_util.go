package allocrunner

import (
	"net"
	"os"

	"github.com/pkg/errors"
)

func openUnixSocket(socketPath, protocol string) (net.Listener, error) {
	// if the socket already exists we'll try to remove it, but if not then any
	// other errors will bubble up to the caller here or when we try to listen
	if _, err := os.Stat(socketPath); err == nil {
		if err := os.Remove(socketPath); err != nil {
			return nil, errors.Wrapf(err, "unable to remove existing unix socket for Consul %s endpoint")
		}
	}

	// create the socket at the given path
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, errors.Wrapf(err, "unable to create unix socket for Consul %s endpoint", protocol)
	}

	// The socket should be usable by all users in case a task is running as an
	// unprivileged user.  Unix does not allow setting domain socket permissions
	// when creating the file, so we must manually call chmod afterwards.
	// https://github.com/golang/go/issues/11822
	if err := os.Chmod(socketPath, os.ModePerm); err != nil {
		_ = listener.Close() // proactively give up
		return nil, errors.Wrapf(err, "unable to set permissions on unix socket for Consul %s endpoint", protocol)
	}

	return listener, nil
}
