package reboot

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/nomad/api"
	"github.com/hashicorp/nomad/e2e/e2eutil"
	"github.com/hashicorp/nomad/e2e/framework"
	"github.com/hashicorp/nomad/helper"
	"github.com/hashicorp/nomad/helper/uuid"
	"github.com/hashicorp/nomad/jobspec"
	"github.com/hashicorp/nomad/testutil"
	"github.com/kr/pretty"
	"github.com/stretchr/testify/require"
)

type RebootE2ETest struct {
	framework.TC
	jobIds []string
}

func init() {
	framework.AddSuites(&framework.TestSuite{
		Component: "Reboot",
		Cases: []framework.TestCase{
			new(RebootE2ETest),
		},
	})
}

func (tc *RebootE2ETest) BeforeAll(f *framework.F) {
	// Ensure cluster has leader before running tests
	e2eutil.WaitForLeader(f.T(), tc.Nomad())
	// Ensure that we have four client nodes in ready state
	e2eutil.WaitForNodesReady(f.T(), tc.Nomad(), 1)
}

// TestReboot_TerminalAlloc asserts that if a job is stopped while the node
// running its allocation is rebooting, the allocation will not be restarted
// when the agent starts.
func (tc *RebootE2ETest) TestReboot_TerminalAlloc(f *framework.F) {
	t := f.T()
	nomadClient := tc.Nomad()

	// Start a service
	sleeperJob, err := jobspec.ParseFile("reboot/input/sleeper.nomad")
	require.NoError(t, err)
	sleeperJob.ID = helper.StringToPtr("sleeper" + uuid.Generate()[0:8])
	sleeperJob.Name = sleeperJob.ID

	t.Logf("Registering service...")
	allocs := e2eutil.RegisterJob(t, nomadClient, sleeperJob)
	require.Len(t, allocs, 1)

	testutil.WaitForResult(func() (bool, error) {
		alloc, _, err := nomadClient.Allocations().Info(allocs[0].ID, nil)
		if err != nil {
			return false, err
		}

		if alloc.ClientStatus != "running" {
			return false, fmt.Errorf("expected alloc to be running: %q", alloc.ClientStatus)
		}

		taskState, ok := alloc.TaskStates["sleeper"]
		if !ok {
			return false, fmt.Errorf("no state for sleeper task")
		}
		if taskState.State != "running" {
			return false, fmt.Errorf("expected state to be running but found %q", taskState.State)
		}
		return taskState.Restarts == 0, fmt.Errorf("unexpected restart: %d\nEvnets:\n%#v", taskState.Restarts, taskState.Events)
	}, func(err error) {
		require.NoError(t, err)
	})

	// Reboot the node
	/*
		rebootJob, err := jobspec.ParseFile("reboot/input/reboot.nomad")
		require.NoError(t, err)
		rebootJob.ID = helper.StringToPtr("reboot" + uuid.Generate()[0:8])
		rebootJob.Name = rebootJob.ID
		rebootJob.Constraints[0].RTarget = allocs[0].NodeID

		resp, _, err := nomadClient.Jobs().Register(rebootJob, nil)
		require.NoError(t, err)
		require.NotZero(t, resp.EvalID)
	*/

	reboot(t, nomadClient, allocs[0].NodeID)

	// Wait for reboot
	t.Logf("Waiting for reboot")

	// Stop the job
	_, _, err = nomadClient.Jobs().Deregister(*sleeperJob.ID, false, nil)
	require.NoError(t, err)

	time.Sleep(10 * time.Second)

	// Make sure alloc is restarted
	testutil.WaitForResult(func() (bool, error) {
		time.Sleep(10 * time.Millisecond)
		alloc, _, err := nomadClient.Allocations().Info(allocs[0].ID, nil)
		if err != nil {
			return false, err
		}

		if alloc.ClientStatus != "running" {
			return false, fmt.Errorf("expected alloc to be running: %q", alloc.ClientStatus)
		}

		taskState, ok := alloc.TaskStates["sleeper"]
		if !ok {
			return false, fmt.Errorf("no state for sleeper task")
		}
		if taskState.State != "running" {
			return false, fmt.Errorf("expected state to be running but found %q", taskState.State)
		}
		return taskState.Restarts == 1, fmt.Errorf("unexpected restart: %d\nEvents:\n%s", taskState.Restarts, pretty.Sprint(taskState.Events))
	}, func(err error) {
		require.NoError(t, err)
	})
}

// reboot a remote EC2 instance via ssh
func reboot(t *testing.T, c *api.Client, nodeID string) {
	node, _, err := c.Nodes().Info(nodeID, nil)
	require.NoError(t, err)

	ip, ok := node.Attributes["unique.platform.aws.public-ipv4"]
	require.True(t, ok, "missing 'unique.platform.aws.public-ipv4' attribute")

	pems, err := filepath.Glob("terraform/keys/*.pem")
	require.NoError(t, err)
	require.Len(t, pems, 1, "expected exactly 1 pem file")

	cmd := exec.Command("ssh",
		"-i", pems[0],
		"-o", "StrictHostKeyChecking=accept-new",
		fmt.Sprintf("ubuntu@%s", ip),
		`sudo reboot && sleep 10`, // sleep to get an error on reboot
	)

	// Command should error because host is rebooting
	out, err := cmd.CombinedOutput()
	require.Error(t, err)
	msg := string(out)
	switch {
	case strings.Contains(msg, "closed by remote host."):
		// Expected error
	case strings.Contains(msg, "Broken pipe"):
		// Expected error
	default:
		require.Failf(t, "unexpected error", msg)
	}
}
