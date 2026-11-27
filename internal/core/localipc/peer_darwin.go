package localipc

import "golang.org/x/sys/unix"

const socketPathLimit = 104

func peerUID(fd int) (uint32, error) {
	peer, err := unix.GetsockoptXucred(fd, unix.SOL_LOCAL, unix.LOCAL_PEERCRED)
	if err != nil {
		return 0, err
	}
	// Darwin's sys/ucred.h defines XUCRED_VERSION as zero.
	if peer.Version != 0 {
		return 0, ErrUnsupported
	}
	return peer.Uid, nil
}
