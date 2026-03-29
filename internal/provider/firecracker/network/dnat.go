package network

import (
	"fmt"
	"os/exec"
	"strconv"
)

// AddDNAT adds iptables rules to forward hostPort to guestIP:targetPort.
// Three rules: PREROUTING DNAT (external), OUTPUT DNAT (local), FORWARD ACCEPT.
func AddDNAT(hostPort int, guestIP string, targetPort int) error {
	dest := guestIP + ":" + strconv.Itoa(targetPort)
	hp := strconv.Itoa(hostPort)
	tp := strconv.Itoa(targetPort)

	comment := "navaris:" + hp
	rules := [][]string{
		{"iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", hp, "-j", "DNAT", "--to-destination", dest, "-m", "comment", "--comment", comment},
		{"iptables", "-t", "nat", "-A", "OUTPUT", "-p", "tcp", "-m", "addrtype", "--dst-type", "LOCAL", "--dport", hp, "-j", "DNAT", "--to-destination", dest, "-m", "comment", "--comment", comment},
		{"iptables", "-A", "FORWARD", "-p", "tcp", "-d", guestIP, "--dport", tp, "-j", "ACCEPT", "-m", "comment", "--comment", comment},
	}

	for _, args := range rules {
		if out, err := exec.Command(args[0], args[1:]...).CombinedOutput(); err != nil {
			// Best-effort rollback: remove any rules we already added.
			RemoveDNAT(hostPort, guestIP, targetPort)
			return fmt.Errorf("add dnat %s→%s: %w: %s", hp, dest, err, out)
		}
	}
	return nil
}

// RemoveDNAT removes the three iptables rules added by AddDNAT.
// Errors are silently ignored (rules may not exist during cleanup).
func RemoveDNAT(hostPort int, guestIP string, targetPort int) {
	dest := guestIP + ":" + strconv.Itoa(targetPort)
	hp := strconv.Itoa(hostPort)
	tp := strconv.Itoa(targetPort)

	comment := "navaris:" + hp
	rules := [][]string{
		{"iptables", "-t", "nat", "-D", "PREROUTING", "-p", "tcp", "--dport", hp, "-j", "DNAT", "--to-destination", dest, "-m", "comment", "--comment", comment},
		{"iptables", "-t", "nat", "-D", "OUTPUT", "-p", "tcp", "-m", "addrtype", "--dst-type", "LOCAL", "--dport", hp, "-j", "DNAT", "--to-destination", dest, "-m", "comment", "--comment", comment},
		{"iptables", "-D", "FORWARD", "-p", "tcp", "-d", guestIP, "--dport", tp, "-j", "ACCEPT", "-m", "comment", "--comment", comment},
	}

	for _, args := range rules {
		exec.Command(args[0], args[1:]...).CombinedOutput()
	}
}
