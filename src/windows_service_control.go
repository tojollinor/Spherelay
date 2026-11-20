package main

type WindowsServiceInfo struct {
	Installed   bool   `json:"installed"`
	State       string `json:"state"`
	StartMode   string `json:"startMode"`
	Account     string `json:"account"`
	BinaryPath  string `json:"binaryPath"`
	BeforeLogin bool   `json:"beforeLogin"`
	Delayed     bool   `json:"delayed"`
}

func validServiceAction(action string) bool {
	switch action {
	case "start", "stop", "restart", "automatic", "manual":
		return true
	}
	return false
}
