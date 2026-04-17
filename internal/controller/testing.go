package controller

import (
	"context"
	"sync"

	"github.com/actions/scaleset"
)

// fakeScaleSetClient is a minimal in-memory ScaleSetClient implementation for tests.
type fakeScaleSetClient struct {
	mu sync.Mutex

	// byName drives Get; nil value means "not found".
	byName map[string]*scaleset.RunnerScaleSet
	// allInGroup is what List returns.
	allInGroup []scaleset.RunnerScaleSet

	createCalls []scaleset.RunnerScaleSet
	updateCalls []scaleset.RunnerScaleSet
	deleteCalls []int

	nextID int

	createErr, getErr, updateErr, deleteErr, listErr error
}

func newFakeScaleSetClient() *fakeScaleSetClient {
	return &fakeScaleSetClient{
		byName: map[string]*scaleset.RunnerScaleSet{},
		nextID: 100,
	}
}

func (f *fakeScaleSetClient) CreateRunnerScaleSet(_ context.Context, rss *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return nil, f.createErr
	}
	cp := *rss
	cp.ID = f.nextID
	f.nextID++
	f.byName[rss.Name] = &cp
	f.createCalls = append(f.createCalls, *rss)
	return &cp, nil
}

func (f *fakeScaleSetClient) GetRunnerScaleSet(_ context.Context, _ int, name string) (*scaleset.RunnerScaleSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.byName[name], nil
}

func (f *fakeScaleSetClient) UpdateRunnerScaleSet(_ context.Context, id int, rss *scaleset.RunnerScaleSet) (*scaleset.RunnerScaleSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.updateErr != nil {
		return nil, f.updateErr
	}
	cp := *rss
	cp.ID = id
	f.byName[rss.Name] = &cp
	f.updateCalls = append(f.updateCalls, *rss)
	return &cp, nil
}

func (f *fakeScaleSetClient) DeleteRunnerScaleSet(_ context.Context, id int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.deleteErr != nil {
		return f.deleteErr
	}
	f.deleteCalls = append(f.deleteCalls, id)
	return nil
}

func (f *fakeScaleSetClient) ListRunnerScaleSets(_ context.Context, _ int) ([]scaleset.RunnerScaleSet, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.listErr != nil {
		return nil, f.listErr
	}
	out := make([]scaleset.RunnerScaleSet, len(f.allInGroup))
	copy(out, f.allInGroup)
	return out, nil
}

func (f *fakeScaleSetClient) MessageSessionClient(_ context.Context, _ int, _ string, _ ...scaleset.HTTPOption) (*scaleset.MessageSessionClient, error) {
	panic("MessageSessionClient not used by tests")
}

func (f *fakeScaleSetClient) GenerateJitRunnerConfig(_ context.Context, _ *scaleset.RunnerScaleSetJitRunnerSetting, _ int) (*scaleset.RunnerScaleSetJitRunnerConfig, error) {
	panic("GenerateJitRunnerConfig not used by tests")
}

func (f *fakeScaleSetClient) GetRunnerByName(_ context.Context, _ string) (*scaleset.RunnerReference, error) {
	panic("GetRunnerByName not used by tests")
}

func (f *fakeScaleSetClient) RemoveRunner(_ context.Context, _ int64) error {
	panic("RemoveRunner not used by tests")
}
