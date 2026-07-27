// PhonicsFun 服务端：自然拼读单词卡应用的单二进制入口。
package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"phonicsfun/internal/config"
	"phonicsfun/internal/httpapi"
	"phonicsfun/internal/llm"
	"phonicsfun/internal/pipeline"
	"phonicsfun/internal/store"
	"phonicsfun/web"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()

	st, err := store.New(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("初始化数据目录 %s: %w", cfg.DataDir, err)
	}
	client, err := llm.New(ctx, cfg)
	if err != nil {
		return err
	}
	pipe := pipeline.New(st, pipeline.WrapClient(client))
	pipe.Start(ctx)
	defer pipe.Stop()
	if err := pipe.Recover(); err != nil {
		return fmt.Errorf("恢复扫描: %w", err)
	}

	dist, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", cfg.Port),
		Handler: httpapi.New(st, pipe, client, dist),
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("PhonicsFun 已启动: http://localhost:%d （数据目录 %s，文本模型 %s，Live 模型 %s）",
			cfg.Port, cfg.DataDir, cfg.TextModel, cfg.LiveModel)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case s := <-sig:
		log.Printf("收到 %v，正在退出...", s)
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
