package server

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

type ServiceState int

const (
	ServiceStateHealthy   ServiceState = 0
	ServiceStateUnhealthy ServiceState = 1
	ServiceStateAdding    ServiceState = 2
	ServiceStateDraining  ServiceState = 3
)

type ServiceStateChangeConsumer interface {
	StateChanged(service *Service)
}

const (
	DefaultDrainTimeout = time.Second * 5
	DefaultAddTimeout   = time.Second * 5
)

func (s ServiceState) String() string {
	switch s {
	case ServiceStateHealthy:
		return "healthy"
	case ServiceStateUnhealthy:
		return "unhealthy"
	case ServiceStateAdding:
		return "adding"
	case ServiceStateDraining:
		return "draining"
	}
	return ""
}

type inflightMap map[*http.Request]context.CancelFunc

type Service struct {
	hostURL      *url.URL
	proxy        *httputil.ReverseProxy
	drainTimeout time.Duration
	addTimeout   time.Duration

	state        ServiceState
	inflight     inflightMap
	inflightLock sync.Mutex

	consumer      ServiceStateChangeConsumer
	healthcheck   *HealthCheck
	becameHealthy chan (bool)
}

func NewService(hostURL *url.URL) *Service {
	service := &Service{
		hostURL:      hostURL,
		drainTimeout: DefaultDrainTimeout,
		addTimeout:   DefaultAddTimeout,

		state:    ServiceStateAdding,
		inflight: inflightMap{},
	}

	service.proxy = &httputil.ReverseProxy{
		Rewrite: service.Rewrite,
	}

	return service
}

func (s *Service) Host() string {
	return s.hostURL.Host
}

func (s *Service) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	req = s.beginInflightRequest(req)
	if req == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	defer s.endInflightRequest(req)

	s.proxy.ServeHTTP(w, req)
}

func (s *Service) Rewrite(req *httputil.ProxyRequest) {
	req.SetURL(s.hostURL)
}

func (s *Service) Drain() {
	s.updateState(ServiceStateDraining)
	if s.healthcheck != nil {
		s.healthcheck.Close()
	}

	deadline := time.After(s.drainTimeout)
	toCancel := s.pendingRequestsToCancel()

WAIT_FOR_REQUESTS_TO_COMPLETE:
	for req := range toCancel {
		select {
		case <-req.Context().Done():
		case <-deadline:
			break WAIT_FOR_REQUESTS_TO_COMPLETE
		}
	}

	for _, cancel := range toCancel {
		cancel()
	}
}

func (s *Service) BeginHealthChecks(consumer ServiceStateChangeConsumer) {
	s.consumer = consumer
	s.becameHealthy = make(chan bool)
	s.healthcheck = NewHealthCheck(s, s.hostURL.JoinPath("up"), time.Second, time.Second*10)
}

func (s *Service) WaitUntilHealthy() bool {
	select {
	case <-time.After(s.addTimeout):
		return false
	case <-s.becameHealthy:
		return true
	}
}

// HealthCheckConsumer

func (s *Service) HealthCheckCompleted(success bool) {
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	oldState := s.state
	if success && (s.state == ServiceStateUnhealthy || s.state == ServiceStateAdding) {
		s.state = ServiceStateHealthy
	}
	if !success && s.state == ServiceStateHealthy {
		s.state = ServiceStateUnhealthy
	}

	if s.state != oldState {
		log.Info().
			Str("host", s.hostURL.Host).
			Stringer("from", oldState).
			Stringer("to", s.state).
			Msg("Service health updated")

		s.consumer.StateChanged(s)

		if s.state == ServiceStateHealthy && oldState == ServiceStateAdding {
			close(s.becameHealthy)
		}
	}
}

// Private

func (s *Service) updateState(state ServiceState) {
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	s.state = state
}

func (s *Service) beginInflightRequest(req *http.Request) *http.Request {
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	if s.state == ServiceStateDraining {
		return nil
	}

	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	s.inflight[req] = cancel
	return req
}

func (s *Service) endInflightRequest(req *http.Request) {
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	cancel := s.inflight[req]
	cancel() // If Drain is waiting on us, let it know we're done

	delete(s.inflight, req)
}

func (s *Service) pendingRequestsToCancel() inflightMap {
	// We use a copy of the inflight map to iterate over while draining, so that
	// we don't need to lock it the whole time, which could interfere with the
	// locking that happens when requests end.
	s.inflightLock.Lock()
	defer s.inflightLock.Unlock()

	result := inflightMap{}
	for k, v := range s.inflight {
		result[k] = v
	}
	return result
}
