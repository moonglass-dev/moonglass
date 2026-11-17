package policy

import "strings"

var defaultSysctls = map[string]bool{
	"net.core.somaxconn":           true,
	"net.ipv4.ip_local_port_range": true,
}

var ipcSysctls = map[string]bool{
	"kernel.msgmax":          true,
	"kernel.msgmnb":          true,
	"kernel.msgmni":          true,
	"kernel.sem":             true,
	"kernel.shmall":          true,
	"kernel.shmmax":          true,
	"kernel.shmmni":          true,
	"kernel.shm_rmid_forced": true,
}

func sysctlNamespaced(name string) bool {
	if strings.HasPrefix(name, "net.") || strings.HasPrefix(name, "fs.mqueue.") {
		return true
	}
	return ipcSysctls[name]
}

func sysctlIPC(name string) bool {
	return strings.HasPrefix(name, "fs.mqueue.") || ipcSysctls[name]
}

func sysctlTreatAsHost(name string, s Spec, env Env) bool {
	if strings.HasPrefix(name, "net.") && sharesHostNetwork(s, env) {
		return true
	}
	if sysctlIPC(name) && strings.EqualFold(s.IPC, "host") {
		return true
	}
	return false
}

func sharesHostNetwork(s Spec, env Env) bool {
	if strings.EqualFold(s.NetworkMode, "host") {
		return true
	}
	if id, ok := containerRef(s.NetworkMode); ok {
		if env.ContainerProject == nil {
			return true // unknown is cross-project
		}
		proj, known := env.ContainerProject(id)
		return !known || proj != s.Project
	}
	return false
}

func containerRef(mode string) (string, bool) {
	const p = "container:"
	if len(mode) > len(p) && strings.EqualFold(mode[:len(p)], p) {
		return mode[len(p):], true
	}
	return "", false
}

func serviceRef(mode string) (string, bool) {
	const p = "service:"
	if len(mode) > len(p) && strings.EqualFold(mode[:len(p)], p) {
		return mode[len(p):], true
	}
	return "", false
}
