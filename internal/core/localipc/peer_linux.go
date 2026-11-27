package localipc

import "golang.org/x/sys/unix"

const socketPathLimit = 108

func peerUID(fd int) (uint32, error) {
	peer, err := unix.GetsockoptUcred(fd, unix.SOL_SOCKET, unix.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return peer.Uid, nil
}
