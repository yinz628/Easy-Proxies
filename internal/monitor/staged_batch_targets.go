package monitor

import (
	"context"
	"errors"
	"fmt"
	"strings"

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

func (s *Server) runManualProbeAndPersist(ctx context.Context, target BatchProbeTarget) (int64, error) {
	if strings.TrimSpace(target.Tag) != "" {
		return s.runManualProbeAndPersistByTag(ctx, target.Tag)
	}
	if s.nodeMgr == nil {
		return -1, errors.New("node manager not initialized")
	}

	runtimeNode, err := s.nodeMgr.CreateConfigNodeRuntime(ctx, target.ConfigNode)
	if err != nil {
		return -1, err
	}
	defer runtimeNode.Close()

	latency, probeErr := runtimeNode.Probe(ctx)
	resultErr := probeErr
	if resultErr == nil {
		if latencyErr := ProbeLatencyError(latency); latencyErr != nil {
			resultErr = manualProbeTimeoutError()
		}
	} else if isManualProbeTimeoutError(resultErr) {
		resultErr = manualProbeTimeoutError()
	}

	if s.store != nil && target.StoreNodeID > 0 {
		if saveErr := s.store.SaveNodeManualProbeResult(ctx, buildManualProbeResult(target.StoreNodeID, latency, resultErr)); saveErr != nil {
			return -1, saveErr
		}
	}

	if resultErr != nil {
		return -1, resultErr
	}
	return normalizeProbeLatencyMS(latency), nil
}
