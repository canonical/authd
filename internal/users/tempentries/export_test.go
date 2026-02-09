package tempentries

type PreAuthUser = preAuthUser

func NewPreAuthUser(uid uint32, loginName string) PreAuthUser {
	return PreAuthUser{uid: uid, loginName: loginName}
}

func (r *PreAuthUserRecords) GetUsers() map[uint32]PreAuthUser {
	r.rwMu.RLock()
	defer r.rwMu.RUnlock()

	return r.users
}

func (r *PreAuthUserRecords) SetTestUsers(users map[uint32]PreAuthUser, uidByLogin map[string]uint32) {
	r.users = users
	r.uidByLogin = uidByLogin
}

func (r *PreAuthUserRecords) DeletePreAuthUser(uid uint32) {
	r.rwMu.Lock()
	defer r.rwMu.Unlock()

	r.deletePreAuthUser(uid)
}
