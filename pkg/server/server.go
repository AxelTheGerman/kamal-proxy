package server

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

const (
	shutdownTimeout = time.Second * 10
)

type Server struct {
	config         Config
	httpServer     *http.Server
	httpListener   net.Listener
	loadBalancer   *LoadBalancer
	commandHandler *CommandHandler
}

func NewServer(c Config) *Server {
	server := &Server{
		config:       c,
		loadBalancer: NewLoadBalancer(),
	}

	server.commandHandler = NewCommandHandler(server.loadBalancer)

	return server
}

func (s *Server) LoadBalancer() *LoadBalancer {
	return s.loadBalancer
}

func (s *Server) Addr() string {
	if s.httpListener == nil {
		return ""
	}
	return s.httpListener.Addr().String()
}

func (s *Server) Start() error {
	log.Info().Msg("Server starting")

	s.httpServer = &http.Server{
		Addr:    s.config.ListenAddress,
		Handler: s.loadBalancer,

		ReadTimeout:  5 * time.Second,
		WriteTimeout: 5 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	var err error
	s.httpListener, err = net.Listen("tcp", s.httpServer.Addr)
	if err != nil {
		return err
	}

	go s.httpServer.Serve(s.httpListener)

	err = s.commandHandler.Start(s.config.SocketPath)
	if err != nil {
		return err
	}

	log.Info().Msg("Server started")
	return nil
}

func (s *Server) Stop() error {
	log.Info().Msg("Server stopping")

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	err := s.httpServer.Shutdown(ctx)
	s.httpServer = nil
	s.httpListener = nil

	s.commandHandler.Stop()

	return err
}

// Private
