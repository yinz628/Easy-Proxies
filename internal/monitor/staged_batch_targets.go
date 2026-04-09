package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"easy_proxies/internal/config"
	"easy_proxies/internal/store"
)

type resolvedManageExecutionTarget struct {
	Tag         string
	Name        string
	StoreNodeID int64
	ConfigNode  config.NodeConfig
}

func (s *Server) resolveBatchProbeTargets(ctx context.Context, rows []ManageRow) ([]BatchProbeTarget, error) {
	resolved, err := s.resolveManageExecutionTargets(ctx, rows)
	if err != nil {
		return nil, err
	}

	targets := make([]BatchProbeTarget, 0, len(resolved))
	for _, target := range resolved {
		targets = append(targets, BatchProbeTarget{
			Tag:         target.Tag,
			Name:        target.Name,
			StoreNodeID: target.StoreNodeID,
			ConfigNode:  target.ConfigNode,
		})
	}
	return targets, nil
}

func (s *Server) resolveBatchQualityTargets(ctx context.Context, rows []ManageRow) ([]BatchQualityTarget, error) {
	resolved, err := s.resolveManageExecutionTargets(ctx, rows)
	if err != nil {
		return nil, err
	}

	targets := make([]BatchQualityTarget, 0, len(resolved))
	for _, target := range resolved {
		targets = append(targets, BatchQualityTarget{
			Tag:         target.Tag,
			Name:        target.Name,
			StoreNodeID: target.StoreNodeID,
			ConfigNode:  target.ConfigNode,
		})
	}
	return targets, nil
}

func (s *Server) resolveManageExecutionTargets(ctx context.Context, rows []ManageRow) ([]resolvedManageExecutionTarget, error) {
	nodesByURI := map[string]*store.Node{}
	nodesByName := map[string]*store.Node{}
	needsStoreLookup := false
	for _, row := range rows {
		if strings.TrimSpace(row.Tag) == "" {
			needsStoreLookup = true
			break
		}
	}

	if needsStoreLookup && s.store != nil {
		storeNodes, err := s.store.ListNodes(ctx, store.NodeFilter{})
		if err != nil {
			return nil, err
		}
		nodesByURI = make(map[string]*store.Node, len(storeNodes))
		nodesByName = make(map[string]*store.Node, len(storeNodes))
		for idx := range storeNodes {
			node := &storeNodes[idx]
			if key := normalizeLookupKey(node.URI); key != "" {
				nodesByURI[key] = node
			}
			if key := normalizeLookupKey(node.Name); key != "" {
				nodesByName[key] = node
			}
		}
	}

	targets := make([]resolvedManageExecutionTarget, 0, len(rows))
	seen := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if tag := strings.TrimSpace(row.Tag); tag != "" {
			key := "tag:" + tag
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			targets = append(targets, resolvedManageExecutionTarget{
				Tag:  tag,
				Name: firstNonEmpty(strings.TrimSpace(row.Name), tag),
			})
			continue
		}

		cfgNode := manageRowToConfigNode(row)
		storeNodeID := int64(0)
		if resolved := resolveStoreNodeForManageRow(row, nodesByURI, nodesByName); resolved != nil {
			storeNodeID = resolved.ID
			cfgNode = storeNodeToConfigNode(resolved)
		}

		key := executionTargetKey(storeNodeID, cfgNode)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, resolvedManageExecutionTarget{
			Name:        firstNonEmpty(strings.TrimSpace(cfgNode.Name), strings.TrimSpace(row.Name), strings.TrimSpace(row.URI), "unknown"),
			StoreNodeID: storeNodeID,
			ConfigNode:  cfgNode,
		})
	}

	return targets, nil
}

func executionTargetKey(storeNodeID int64, node config.NodeConfig) string {
	if storeNodeID > 0 {
		return fmt.Sprintf("store:%d", storeNodeID)
	}
	if uri := normalizeLookupKey(node.URI); uri != "" {
		return "uri:" + uri
	}
	if name := normalizeLookupKey(node.Name); name != "" {
		return "name:" + name
	}
	return ""
}

func manageRowToConfigNode(row ManageRow) config.NodeConfig {
	return config.NodeConfig{
		Name:           strings.TrimSpace(row.Name),
		URI:            strings.TrimSpace(row.URI),
		Port:           row.Port,
		Username:       row.Username,
		Password:       row.Password,
		Source:         config.NodeSource(strings.TrimSpace(row.Source)),
		Disabled:       row.Disabled,
		LifecycleState: strings.TrimSpace(row.LifecycleState),
	}
}

func storeNodeToConfigNode(node *store.Node) config.NodeConfig {
	if node == nil {
		return config.NodeConfig{}
	}
	return config.NodeConfig{
		Name:           strings.TrimSpace(node.Name),
		URI:            strings.TrimSpace(node.URI),
		Port:           node.Port,
		Username:       node.Username,
		Password:       node.Password,
		Source:         config.NodeSource(strings.TrimSpace(node.Source)),
		Disabled:       node.LifecycleState == store.NodeLifecycleDisabled,
		LifecycleState: strings.TrimSpace(node.LifecycleState),
		FeedKey:        strings.TrimSpace(node.FeedKey),
	}
}

func (s *Server) runBatchProbeTarget(ctx context.Context, target BatchProbeTarget) (int64, error) {
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	done := make(chan struct{})
	defer close(done)

	if target.StoreNodeID > 0 {
		go func() {
			select {
			case <-probeCtx.Done():
				if resultErr := normalizeManualProbeContextError(probeCtx.Err()); resultErr != nil {
					_ = s.saveManualProbeResultIfPossible(probeCtx, target.StoreNodeID, 0, resultErr)
				}
			case <-done:
			}
		}()
	}

	if err := s.probeSem.Acquire(probeCtx, 1); err != nil {
		return -1, fmt.Errorf("probe cancelled: %w", err)
	}
	defer s.probeSem.Release(1)

	return s.runManualProbeAndPersist(probeCtx, target)
}

func (s *Server) runManualProbeAndPersist(ctx context.Context, target BatchProbeTarget) (int64, error) {
	if strings.TrimSpace(target.Tag) != "" {
		return s.runManualProbeAndPersistByTag(ctx, target.Tag)
	}
	if s.nodeMgr == nil {
		err := errors.New("node manager not initialized")
		if saveErr := s.saveManualProbeResultIfPossible(ctx, target.StoreNodeID, 0, err); saveErr != nil {
			return -1, saveErr
		}
		return -1, err
	}

	runtimeNode, err := s.nodeMgr.CreateConfigNodeRuntime(ctx, target.ConfigNode)
	if err != nil {
		if saveErr := s.saveManualProbeResultIfPossible(ctx, target.StoreNodeID, 0, err); saveErr != nil {
			return -1, saveErr
		}
		return -1, err
	}
	var closeOnce sync.Once
	closeRuntime := func() {
		closeOnce.Do(func() {
			_ = runtimeNode.Close()
		})
	}
	defer closeRuntime()

	var persistOnce sync.Once
	var persistErr error
	persistResult := func(latency time.Duration, resultErr error) error {
		persistOnce.Do(func() {
			persistErr = s.saveManualProbeResultIfPossible(ctx, target.StoreNodeID, latency, resultErr)
		})
		return persistErr
	}

	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			closeRuntime()
			if resultErr := normalizeManualProbeContextError(ctx.Err()); resultErr != nil {
				_ = persistResult(0, resultErr)
			}
		case <-done:
		}
	}()

	latency, probeErr := runtimeNode.Probe(ctx)
	resultErr := probeErr
	if resultErr == nil {
		if latencyErr := ProbeLatencyError(latency); latencyErr != nil {
			resultErr = manualProbeTimeoutError()
		}
	} else if isManualProbeTimeoutError(resultErr) {
		resultErr = manualProbeTimeoutError()
	}

	if saveErr := persistResult(latency, resultErr); saveErr != nil {
		return -1, saveErr
	}

	if resultErr != nil {
		return -1, resultErr
	}
	return normalizeProbeLatencyMS(latency), nil
}

func (s *Server) saveManualProbeResultIfPossible(ctx context.Context, nodeID int64, latency time.Duration, err error) error {
	if s == nil || s.store == nil || nodeID <= 0 {
		return nil
	}
	saveCtx, cancel := manualProbePersistContext(ctx)
	defer cancel()
	return s.store.SaveNodeManualProbeResult(saveCtx, buildManualProbeResult(nodeID, latency, err))
}

func manualProbePersistContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx != nil && ctx.Err() == nil {
		return context.WithTimeout(ctx, 5*time.Second)
	}
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func normalizeManualProbeContextError(err error) error {
	if err == nil {
		return nil
	}
	if isManualProbeTimeoutError(err) {
		return manualProbeTimeoutError()
	}
	return err
}
