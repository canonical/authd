package permissions

type PeerAuthInfo = peerAuthInfo

func NewTestPeerAuthInfo(uid uint32, pid int32) PeerAuthInfo {
	return PeerAuthInfo{uid: uid, pid: pid}
}

var (
	CurrentUserUID = currentUserUID
)
