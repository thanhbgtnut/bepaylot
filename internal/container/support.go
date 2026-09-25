package container

import (
	"context"
	"log/slog"

	"github.com/hibiken/asynq"

	"github.com/thanhenti/bepaylot/internal/application/repository/postgres"
	"github.com/thanhenti/bepaylot/internal/config"
	"github.com/thanhenti/bepaylot/internal/handler/dto"
	appmcp "github.com/thanhenti/bepaylot/internal/mcp"
	"github.com/thanhenti/bepaylot/internal/queue"
	"github.com/thanhenti/bepaylot/internal/types"
)

// bootstrapMCP attaches every MCP server known at startup: first the ones
// declared in the config file, then the ones added previously through the API
// (persisted in the database). Per-server failures are logged, not fatal.
func bootstrapMCP(ctx context.Context, m *appmcp.Manager, st *postgres.Store, cfg *config.Config, log *slog.Logger) {
	if cfg.MCP.File != "" {
		specs, err := config.LoadMCPServers(cfg.MCP.File)
		if err != nil {
			log.Warn("mcp: load config file failed", "path", cfg.MCP.File, "err", err)
		} else {
			m.AttachAll(ctx, specs, appmcp.SourceFile)
		}
	}
	rows, err := st.MCP.List(ctx)
	if err != nil {
		log.Warn("mcp: load persisted servers failed", "err", err)
		return
	}
	var specs []config.MCPServerSpec
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		s, err := config.MCPServerSpecFromMap(row.Spec)
		if err != nil {
			log.Warn("mcp: skipping malformed persisted server", "name", row.Name, "err", err)
			continue
		}
		specs = append(specs, s)
	}
	m.AttachAll(ctx, specs, appmcp.SourceAPI)
}

// asynqInspector reports asynq queue depths per the queue topology.
type asynqInspector struct{ in *asynq.Inspector }

func newAsynqInspector(cfg *config.Config) asynqInspector {
	return asynqInspector{in: asynq.NewInspector(queue.RedisOpt(cfg.Redis))}
}

func (a asynqInspector) Stats(context.Context) []dto.QueueStat {
	var out []dto.QueueStat
	for _, d := range types.QueueDefinitions() {
		st := dto.QueueStat{Name: d.Name, Pool: d.Pool, Weight: d.Weight}
		info, err := a.in.GetQueueInfo(d.Name)
		if err != nil {
			st.Error = err.Error()
		} else {
			st.Size, st.Pending, st.Active, st.Scheduled, st.Retry, st.Archived =
				info.Size, info.Pending, info.Active, info.Scheduled, info.Retry, info.Archived
		}
		out = append(out, st)
	}
	return out
}

// inlineInspector reports the in-process queue.
type inlineInspector struct{ q *queue.Inline }

func (i inlineInspector) Stats(context.Context) []dto.QueueStat {
	return []dto.QueueStat{{Name: "inline", Pool: "inline", Weight: 1, Size: i.q.Pending(), Pending: i.q.Pending()}}
}
