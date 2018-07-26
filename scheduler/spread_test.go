package scheduler

import (
	"testing"

	"fmt"

	"github.com/hashicorp/nomad/helper/uuid"
	"github.com/hashicorp/nomad/nomad/mock"
	"github.com/hashicorp/nomad/nomad/structs"
	"github.com/stretchr/testify/require"
)

func TestSpreadIterator_SingleAttribute(t *testing.T) {
	state, ctx := testContext(t)
	dcs := []string{"dc1", "dc2", "dc1", "dc1"}
	var nodes []*RankedNode

	// Add these nodes to the state store
	for i, dc := range dcs {
		node := mock.Node()
		node.Datacenter = dc
		if err := state.UpsertNode(uint64(100+i), node); err != nil {
			t.Fatalf("failed to upsert node: %v", err)
		}
		nodes = append(nodes, &RankedNode{Node: node})
	}

	static := NewStaticRankIterator(ctx, nodes)

	job := mock.Job()
	tg := job.TaskGroups[0]
	job.TaskGroups[0].Count = 5
	// add allocs to nodes in dc1
	upserting := []*structs.Allocation{
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			EvalID:    uuid.Generate(),
			NodeID:    nodes[0].Node.ID,
		},
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			EvalID:    uuid.Generate(),
			NodeID:    nodes[2].Node.ID,
		},
	}

	if err := state.UpsertAllocs(1000, upserting); err != nil {
		t.Fatalf("failed to UpsertAllocs: %v", err)
	}

	// Create spread target of 80% in dc1
	// Implicitly, this means 20% in dc2
	spread := &structs.Spread{
		Weight:    100,
		Attribute: "${node.datacenter}",
		SpreadTarget: []*structs.SpreadTarget{
			{
				Value:   "dc1",
				Percent: 80,
			},
		},
	}
	tg.Spreads = []*structs.Spread{spread}
	spreadIter := NewSpreadIterator(ctx, static)
	spreadIter.SetJob(job)
	spreadIter.SetTaskGroup(tg)

	scoreNorm := NewScoreNormalizationIterator(ctx, spreadIter)

	out := collectRanked(scoreNorm)

	// Expect nodes in dc1 with existing allocs to get a boost
	// Boost should be ((desiredCount-actual)/expected)*spreadWeight
	// For this test, that becomes dc1 = ((4-2)/4 ) = 0.5, and dc2=(1-0)/1
	expectedScores := map[string]float64{
		"dc1": 0.5,
		"dc2": 1.0,
	}
	for _, rn := range out {
		require.Equal(t, expectedScores[rn.Node.Datacenter], rn.FinalScore)
	}

	// Update the plan to add more allocs to nodes in dc1
	// After this step there are enough allocs to meet the desired count in dc1
	ctx.plan.NodeAllocation[nodes[0].Node.ID] = []*structs.Allocation{
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			NodeID:    nodes[0].Node.ID,
		},
		// Should be ignored as it is a different job.
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: "bbb",
			JobID:     "ignore 2",
			Job:       job,
			ID:        uuid.Generate(),
			NodeID:    nodes[0].Node.ID,
		},
	}
	ctx.plan.NodeAllocation[nodes[3].Node.ID] = []*structs.Allocation{
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			NodeID:    nodes[3].Node.ID,
		},
	}

	// Reset the scores
	for _, node := range nodes {
		node.Scores = nil
		node.FinalScore = 0
	}
	static = NewStaticRankIterator(ctx, nodes)
	spreadIter = NewSpreadIterator(ctx, static)
	spreadIter.SetJob(job)
	spreadIter.SetTaskGroup(tg)
	scoreNorm = NewScoreNormalizationIterator(ctx, spreadIter)
	out = collectRanked(scoreNorm)

	// Expect nodes in dc2 with existing allocs to get a boost
	// DC1 nodes are not boosted because there are enough allocs to meet
	// the desired count
	expectedScores = map[string]float64{
		"dc1": 0,
		"dc2": 1.0,
	}
	for _, rn := range out {
		require.Equal(t, expectedScores[rn.Node.Datacenter], rn.FinalScore)
	}
}

func TestSpreadIterator_MultipleAttributes(t *testing.T) {
	state, ctx := testContext(t)
	dcs := []string{"dc1", "dc2", "dc1", "dc1"}
	rack := []string{"r1", "r1", "r2", "r2"}
	var nodes []*RankedNode

	// Add these nodes to the state store
	for i, dc := range dcs {
		node := mock.Node()
		node.Datacenter = dc
		node.Meta["rack"] = rack[i]
		if err := state.UpsertNode(uint64(100+i), node); err != nil {
			t.Fatalf("failed to upsert node: %v", err)
		}
		nodes = append(nodes, &RankedNode{Node: node})
	}

	static := NewStaticRankIterator(ctx, nodes)

	job := mock.Job()
	tg := job.TaskGroups[0]
	job.TaskGroups[0].Count = 5
	// add allocs to nodes in dc1
	upserting := []*structs.Allocation{
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			EvalID:    uuid.Generate(),
			NodeID:    nodes[0].Node.ID,
		},
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			EvalID:    uuid.Generate(),
			NodeID:    nodes[2].Node.ID,
		},
	}

	if err := state.UpsertAllocs(1000, upserting); err != nil {
		t.Fatalf("failed to UpsertAllocs: %v", err)
	}

	spread1 := &structs.Spread{
		Weight:    100,
		Attribute: "${node.datacenter}",
		SpreadTarget: []*structs.SpreadTarget{
			{
				Value:   "dc1",
				Percent: 60,
			},
			{
				Value:   "dc2",
				Percent: 40,
			},
		},
	}

	spread2 := &structs.Spread{
		Weight:    50,
		Attribute: "${meta.rack}",
		SpreadTarget: []*structs.SpreadTarget{
			{
				Value:   "r1",
				Percent: 40,
			},
			{
				Value:   "r2",
				Percent: 60,
			},
		},
	}

	tg.Spreads = []*structs.Spread{spread1, spread2}
	spreadIter := NewSpreadIterator(ctx, static)
	spreadIter.SetJob(job)
	spreadIter.SetTaskGroup(tg)

	scoreNorm := NewScoreNormalizationIterator(ctx, spreadIter)

	out := collectRanked(scoreNorm)

	// Score come from combining two different spread factors
	// Second node should have the highest score because it has no allocs and its in dc2/r1
	expectedScores := map[string]float64{
		nodes[0].Node.ID: 0.389,
		nodes[1].Node.ID: 0.833,
		nodes[2].Node.ID: 0.444,
		nodes[3].Node.ID: 0.444,
	}
	for _, rn := range out {
		require.Equal(t, fmt.Sprintf("%.3f", expectedScores[rn.Node.ID]), fmt.Sprintf("%.3f", rn.FinalScore))
	}

}

func TestSpreadIterator_EvenSpread(t *testing.T) {
	state, ctx := testContext(t)
	dcs := []string{"dc1", "dc2", "dc1", "dc2", "dc1", "dc2", "dc2", "dc1", "dc1", "dc1"}
	var nodes []*RankedNode

	// Add these nodes to the state store
	for i, dc := range dcs {
		node := mock.Node()
		node.Datacenter = dc
		if err := state.UpsertNode(uint64(100+i), node); err != nil {
			t.Fatalf("failed to upsert node: %v", err)
		}
		nodes = append(nodes, &RankedNode{Node: node})
	}

	static := NewStaticRankIterator(ctx, nodes)
	job := mock.Job()
	tg := job.TaskGroups[0]
	job.TaskGroups[0].Count = 10

	// Configure even spread across node.datacenter
	spread := &structs.Spread{
		Weight:    100,
		Attribute: "${node.datacenter}",
	}
	tg.Spreads = []*structs.Spread{spread}
	spreadIter := NewSpreadIterator(ctx, static)
	spreadIter.SetJob(job)
	spreadIter.SetTaskGroup(tg)

	scoreNorm := NewScoreNormalizationIterator(ctx, spreadIter)

	out := collectRanked(scoreNorm)

	// Nothing placed so both dc nodes get 0 as the score
	expectedScores := map[string]float64{
		"dc1": 0,
		"dc2": 0,
	}
	for _, rn := range out {
		require.Equal(t, expectedScores[rn.Node.Datacenter], rn.FinalScore)
	}

	// Update the plan to add allocs to nodes in dc1
	// After this step dc2 nodes should get boosted
	ctx.plan.NodeAllocation[nodes[0].Node.ID] = []*structs.Allocation{
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			NodeID:    nodes[0].Node.ID,
		},
	}
	ctx.plan.NodeAllocation[nodes[2].Node.ID] = []*structs.Allocation{
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			NodeID:    nodes[2].Node.ID,
		},
	}

	// Reset the scores
	for _, node := range nodes {
		node.Scores = nil
		node.FinalScore = 0
	}
	static = NewStaticRankIterator(ctx, nodes)
	spreadIter = NewSpreadIterator(ctx, static)
	spreadIter.SetJob(job)
	spreadIter.SetTaskGroup(tg)
	scoreNorm = NewScoreNormalizationIterator(ctx, spreadIter)
	out = collectRanked(scoreNorm)

	// Expect nodes in dc2 with existing allocs to get a boost
	// dc1 nodes are penalized because they have allocs
	expectedScores = map[string]float64{
		"dc1": -0.5,
		"dc2": 0.01,
	}
	for _, rn := range out {
		require.Equal(t, expectedScores[rn.Node.Datacenter], rn.FinalScore)
	}

	// Update the plan to add more allocs to nodes in dc2
	// After this step dc1 nodes should get boosted
	ctx.plan.NodeAllocation[nodes[1].Node.ID] = []*structs.Allocation{
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			NodeID:    nodes[1].Node.ID,
		},
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			NodeID:    nodes[1].Node.ID,
		},
	}
	ctx.plan.NodeAllocation[nodes[3].Node.ID] = []*structs.Allocation{
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			NodeID:    nodes[3].Node.ID,
		},
	}

	// Reset the scores
	for _, node := range nodes {
		node.Scores = nil
		node.FinalScore = 0
	}
	static = NewStaticRankIterator(ctx, nodes)
	spreadIter = NewSpreadIterator(ctx, static)
	spreadIter.SetJob(job)
	spreadIter.SetTaskGroup(tg)
	scoreNorm = NewScoreNormalizationIterator(ctx, spreadIter)
	out = collectRanked(scoreNorm)

	// Expect nodes in dc2 to be penalized because there are 3 allocs there now
	// dc1 nodes are boosted because that has 2 allocs
	expectedScores = map[string]float64{
		"dc1": 0.01,
		"dc2": -0.5,
	}
	for _, rn := range out {
		require.Equal(t, expectedScores[rn.Node.Datacenter], rn.FinalScore)
	}

	// Add another node in dc3
	node := mock.Node()
	node.Datacenter = "dc3"
	if err := state.UpsertNode(uint64(1111), node); err != nil {
		t.Fatalf("failed to upsert node: %v", err)
	}
	nodes = append(nodes, &RankedNode{Node: node})

	// Add another alloc to dc1, now its count matches dc2
	ctx.plan.NodeAllocation[nodes[4].Node.ID] = []*structs.Allocation{
		{
			Namespace: structs.DefaultNamespace,
			TaskGroup: tg.Name,
			JobID:     job.ID,
			Job:       job,
			ID:        uuid.Generate(),
			NodeID:    nodes[4].Node.ID,
		},
	}

	// Reset scores
	for _, node := range nodes {
		node.Scores = nil
		node.FinalScore = 0
	}
	static = NewStaticRankIterator(ctx, nodes)
	spreadIter = NewSpreadIterator(ctx, static)
	spreadIter.SetJob(job)
	spreadIter.SetTaskGroup(tg)
	scoreNorm = NewScoreNormalizationIterator(ctx, spreadIter)
	out = collectRanked(scoreNorm)

	// Expect dc1 and dc2 to be penalized because they have 3 allocs
	// dc3 should get a boost because it has 0 allocs
	expectedScores = map[string]float64{
		"dc1": -0.5,
		"dc2": -0.5,
		"dc3": 0.01,
	}
	for _, rn := range out {
		require.Equal(t, expectedScores[rn.Node.Datacenter], rn.FinalScore)
	}

}
