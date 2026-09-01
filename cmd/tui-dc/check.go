package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/tui-tools/tui-dc/internal/directory"
	"github.com/tui-tools/tui-kit/compat"
)

// checkTimeout bounds the whole read. It is generous: --check runs nine
// samba-tool subcommands, each of which is a Python process that opens the
// directory database and sometimes the network, and a controller that is
// mid-election is slower than one that is not.
const checkTimeout = 3 * time.Minute

// partitionReport is one naming context's replication status, flattened into
// the fields a shell script can assert on without walking the model.
type partitionReport struct {
	DN        string `json:"dn"`
	Direction string `json:"direction"`
	Neighbor  string `json:"neighbor,omitempty"`
	OK        bool   `json:"ok"`
	Failures  int    `json:"failures"`
}

// checkReport is what --check prints: whether there is a samba-tool here at
// all, whether a controller answered, what this host's role is, the counts,
// the replication verdict, and the model in full.
//
// It is a report of the read path only. --check never builds and never runs a
// mutation: the whole point is that it is safe to run anywhere, including in
// CI against a production-shaped machine.
type checkReport struct {
	Tool    string `json:"tool"`
	Version string `json:"version"`
	Backend string `json:"backend"`
	// Describe is the backend's own one-line summary, which is where the demo
	// backend says it is a demo.
	Describe string `json:"describe"`

	// Installed is the first question, and a false one is a normal machine
	// rather than a failure: Detail says why.
	Installed bool   `json:"installed"`
	Detail    string `json:"detail,omitempty"`
	// SambaVersion is what samba-tool printed about itself.
	SambaVersion string `json:"sambaVersion,omitempty"`

	// Reachable reports that a domain controller answered `domain info`, and
	// IsDC that this host's own configuration says it is one. They are
	// different questions and a machine can answer yes to either alone.
	Reachable bool   `json:"domainReachable"`
	IsDC      bool   `json:"isDomainController"`
	Role      string `json:"serverRole,omitempty"`

	Realm       string `json:"realm,omitempty"`
	NetBIOS     string `json:"netbiosDomain,omitempty"`
	DCName      string `json:"dcName,omitempty"`
	ForestLevel string `json:"forestLevel,omitempty"`
	DomainLevel string `json:"domainLevel,omitempty"`
	DNSBackend  string `json:"dnsBackend,omitempty"`

	// The counts, which are what a smoke test asserts on before it asserts on
	// any particular account.
	Users     int `json:"users"`
	Groups    int `json:"groups"`
	Computers int `json:"computers"`
	Records   int `json:"dnsRecords"`

	// PasswordPolicyRead reports that `domain passwordsettings show` ran and
	// was parsed. It is only attempted on a domain controller.
	PasswordPolicyRead bool `json:"passwordPolicyRead"`

	// ZoneRead and ReplicationRead report that those two commands ran at all,
	// so an empty zone and an unqueryable one do not look the same.
	ZoneRead        bool `json:"zoneRead"`
	ReplicationRead bool `json:"replicationRead"`
	// ReplicationOK is false when any partition's last attempt failed. It is
	// meaningful only when ReplicationRead is true.
	ReplicationOK bool              `json:"replicationOk"`
	Partitions    []partitionReport `json:"partitions,omitempty"`

	// UserList, GroupList and ComputerList are the names, in the order the
	// screens show them. The per-account detail is not here: reading it means
	// one samba-tool process per account, which --check has no business doing
	// to a domain with five hundred of them.
	UserList     []string `json:"userList,omitempty"`
	GroupList    []string `json:"groupList,omitempty"`
	ComputerList []string `json:"computerList,omitempty"`

	// Notes are the one-line reasons a part of the read did not happen.
	Notes []string `json:"notes,omitempty"`

	// Compat is what the version probe found. It is reported rather than
	// asserted: an untested version is a fact about the machine, not a failure
	// of the read path.
	Compat compat.Result `json:"compat"`
	// Model is the parsed state in full.
	Model directory.Model `json:"model"`
}

// runCheck exercises the backend's real read path and prints what it parsed as
// JSON. It returns an error when the backend cannot be read at all, which main
// turns into a non-zero exit — so a caller can treat the exit code alone as the
// verdict.
//
// A machine with no Samba on it is not a failure and never has been: most
// machines are like that, and `"installed": false` with the reason beside it is
// the true answer for them. Neither is a machine that has samba-tool but is not
// a domain controller: `"domainReachable": false` is what that machine is.
func runCheck(backend directory.Backend, backendCompat compat.Result,
	out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), checkTimeout)
	defer cancel()

	model, err := backend.Load(ctx)
	if err != nil {
		return fmt.Errorf("%s backend read failed: %w", backend.Name(), err)
	}

	counts := model.Count()
	report := checkReport{
		Tool:               toolName,
		Version:            version,
		Backend:            backend.Name(),
		Describe:           backend.Describe(),
		Installed:          model.Installed,
		Detail:             model.Detail,
		SambaVersion:       model.Version,
		Reachable:          model.Reachable,
		IsDC:               model.Domain.IsDC(),
		Role:               model.Domain.ServerRole,
		Realm:              model.Domain.Realm,
		NetBIOS:            model.Domain.NetBIOS,
		DCName:             model.Domain.DCName,
		ForestLevel:        model.Domain.ForestLevel,
		DomainLevel:        model.Domain.DomainLevel,
		DNSBackend:         model.Domain.DNSBackend,
		Users:              counts.Users,
		Groups:             counts.Groups,
		Computers:          counts.Computers,
		Records:            counts.Records,
		PasswordPolicyRead: model.Policy.Read,
		ZoneRead:           model.Zone.Read,
		ReplicationRead:    model.Repl.Read,
		ReplicationOK:      model.Repl.OK(),
		Notes:              model.Notes,
		Compat:             backendCompat,
		Model:              model,
	}

	for _, user := range model.Users {
		report.UserList = append(report.UserList, user.Name)
	}
	for _, group := range model.Groups {
		report.GroupList = append(report.GroupList, group.Name)
	}
	for _, computer := range model.Computers {
		report.ComputerList = append(report.ComputerList, computer.Name)
	}
	for _, partition := range model.Repl.Partitions {
		report.Partitions = append(report.Partitions, partitionReport{
			DN:        partition.DN,
			Direction: partition.Direction,
			Neighbor:  partition.Neighbor,
			OK:        partition.OK,
			Failures:  partition.Failures,
		})
	}

	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}
