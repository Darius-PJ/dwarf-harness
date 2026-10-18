package graphfoundation

import (
	"context"

	"github.com/Darius-PJ/dwarf-harness/internal/core/ledgerstore"
	"github.com/Darius-PJ/dwarf-harness/internal/core/workspace"
	"github.com/Darius-PJ/dwarf-harness/internal/undercurrent/graph"
)

func ArtifactReader(service *workspace.Service) func(context.Context, graph.ArtifactRequest) (graph.ArtifactBatch, error) {
	return func(ctx context.Context, request graph.ArtifactRequest) (graph.ArtifactBatch, error) {
		batch, err := service.ReadArtifacts(ctx, workspace.ArtifactRequest{WorkspaceID: request.WorkspaceID, Tree: request.Tree, AfterPath: request.AfterPath, MaxBytes: request.MaxBytes, WorkAllowance: request.WorkAllowance, ListOnly: request.ListOnly, Paths: request.Paths})
		result := graph.ArtifactBatch{NextPath: batch.NextPath, Complete: batch.Complete, WorkUsed: batch.WorkUsed, Reason: batch.Reason}
		for _, entry := range batch.Entries {
			result.Entries = append(result.Entries, graph.Artifact{Path: entry.Path, Mode: entry.Mode, GitOID: entry.GitOID, SHA256: entry.SHA256, Bytes: entry.Bytes, Reason: entry.Reason})
		}
		return result, err
	}
}

func Options(store *ledgerstore.Store, service *workspace.Service, tools graph.ToolConfig) graph.Options {
	return graph.Options{Colony: store.Colony(), LogDir: store.LogDir(), Appender: store, Durable: store.Durable, Tools: tools, ReadArtifacts: ArtifactReader(service)}
}
