package password

type Manager struct{}

func New() *Manager {
	panic("TODO: implement password.New")
}

func (m *Manager) Hash(plain string) (string, error) {
	panic("TODO: implement Manager.Hash")
}

func (m *Manager) Compare(hash, plain string) error {
	panic("TODO: implement Manager.Compare")
}
