package op_e2e

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum-optimism/optimism/op-conductor/consensus"
)

// [Category: Initial Setup]
// In this test, we test that we can successfully setup a working cluster.
func TestSequencerFailover_SetupCluster(t *testing.T) {
	sys, conductors := setupSequencerFailoverTest(t)
	defer sys.Close()

	require.Equal(t, 3, len(conductors), "Expected 3 conductors")
	for _, con := range conductors {
		require.NotNil(t, con, "Expected conductor to be non-nil")
	}
}

// [Category: conductor rpc]
// In this test, we test all rpcs exposed by conductor.
func TestSequencerFailover_ConductorRPC(t *testing.T) {
	ctx := context.Background()
	sys, conductors := setupSequencerFailoverTest(t)
	defer sys.Close()

	// SequencerHealthy, Leader, AddServerAsVoter are used in setup already.

	// Test ClusterMembership
	t.Log("Testing ClusterMembership")
	c1 := conductors[Sequencer1Name]
	c2 := conductors[Sequencer2Name]
	c3 := conductors[Sequencer3Name]
	membership, err := c1.client.ClusterMembership(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, len(membership), "Expected 3 members in cluster")
	ids := make([]string, 0)
	for _, member := range membership {
		ids = append(ids, member.ID)
		require.Equal(t, consensus.Voter, member.Suffrage, "Expected all members to be voters")
	}
	sort.Strings(ids)
	require.Equal(t, []string{Sequencer1Name, Sequencer2Name, Sequencer3Name}, ids, "Expected all sequencers to be in cluster")

	// Test Active & Pause & Resume
	t.Log("Testing Active & Pause & Resume")
	active, err := c1.client.Active(ctx)
	require.NoError(t, err)
	require.True(t, active, "Expected conductor to be active")

	err = c1.client.Pause(ctx)
	require.NoError(t, err)
	active, err = c1.client.Active(ctx)
	require.NoError(t, err)
	require.False(t, active, "Expected conductor to be paused")

	err = c1.client.Resume(ctx)
	require.NoError(t, err)
	active, err = c1.client.Active(ctx)
	require.NoError(t, err)
	require.True(t, active, "Expected conductor to be active")

	// Test LeaderWithID
	t.Log("Testing LeaderWithID")
	leader1, err := c1.client.LeaderWithID(ctx)
	require.NoError(t, err)
	leader2, err := c2.client.LeaderWithID(ctx)
	require.NoError(t, err)
	leader3, err := c3.client.LeaderWithID(ctx)
	require.NoError(t, err)
	require.Equal(t, leader1.ID, leader2.ID, "Expected leader ID to be the same")
	require.Equal(t, leader1.ID, leader3.ID, "Expected leader ID to be the same")

	// Test TransferLeader & TransferLeaderToServer
	t.Log("Testing TransferLeader")
	lid, leader := findLeader(t, conductors)
	err = leader.client.TransferLeader(ctx)
	require.NoError(t, err, "Expected leader to transfer leadership")
	require.NoError(t, waitForLeadershipChange(t, leader, false))
	newLeader, err := leader.client.LeaderWithID(ctx)
	require.NoError(t, err)
	isLeader, err := leader.client.Leader(ctx)
	require.NoError(t, err)
	require.False(t, isLeader, "Expected leader to transfer leadership")
	require.NotEqual(t, lid, newLeader.ID, "Expected leader to change")
	require.NoError(t, waitForSequencerStatusChange(t, sys.RollupClient(newLeader.ID), true))

	// old leader now became follower, we're trying to transfer leadership directly back to it.
	t.Log("Testing TransferLeaderToServer")
	fid, follower := lid, leader
	_, leader = findLeader(t, conductors)
	err = leader.client.TransferLeaderToServer(ctx, fid, follower.ConsensusEndpoint())
	require.NoError(t, err, "Expected leader to transfer leadership to follower")
	require.NoError(t, waitForLeadershipChange(t, follower, true))
	require.NoError(t, waitForSequencerStatusChange(t, sys.RollupClient(fid), true))
	leader = follower

	// Test AddServerAsNonvoter, do not start a new sequencer just for this purpose, use Sequencer3's rpc to start conductor.
	// This is fine as this mainly tests conductor's ability to add itself into the raft consensus cluster as a nonvoter.
	t.Log("Testing AddServerAsNonvoter")
	nonvoter := setupConductor(
		t, VerifierName, t.TempDir(),
		sys.RollupEndpoint(Sequencer3Name),
		sys.NodeEndpoint(Sequencer3Name),
		findAvailablePort(t),
		false,
		*sys.RollupConfig,
	)

	err = leader.client.AddServerAsNonvoter(ctx, VerifierName, nonvoter.ConsensusEndpoint())
	require.NoError(t, err, "Expected leader to add non-voter")
	membership, err = leader.client.ClusterMembership(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, len(membership), "Expected 4 members in cluster")
	require.Equal(t, consensus.Nonvoter, membership[3].Suffrage, "Expected last member to be non-voter")

	t.Log("Testing RemoveServer")
	lid, leader = findLeader(t, conductors)
	fid, follower = findFollower(t, conductors)
	err = follower.client.RemoveServer(ctx, lid)
	require.ErrorContains(t, err, "node is not the leader", "Expected follower to fail to remove leader")
	membership, err = c1.client.ClusterMembership(ctx)
	require.NoError(t, err)
	require.Equal(t, 4, len(membership), "Expected 4 members in cluster")

	err = leader.client.RemoveServer(ctx, VerifierName)
	require.NoError(t, err, "Expected leader to remove non-voter")
	membership, err = c1.client.ClusterMembership(ctx)
	require.NoError(t, err)
	require.Equal(t, 3, len(membership), "Expected 2 members in cluster after removal")
	require.NotContains(t, membership, VerifierName, "Expected follower to be removed from cluster")

	err = leader.client.RemoveServer(ctx, fid)
	require.NoError(t, err, "Expected leader to remove follower")
	membership, err = c1.client.ClusterMembership(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, len(membership), "Expected 2 members in cluster after removal")
	require.NotContains(t, membership, fid, "Expected follower to be removed from cluster")
}
