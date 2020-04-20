package server

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/cnrancher/edge-api-server/pkg/auth"
	"github.com/cnrancher/edge-api-server/pkg/controllers"
	"github.com/cnrancher/edge-api-server/pkg/steve/pkg/catalogapi"
	steveserver "github.com/rancher/steve/pkg/server"
	"github.com/rancher/wrangler/pkg/ratelimit"
	"github.com/sirupsen/logrus"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	restclient "k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

type Config struct {
	Namespace       string
	Threadiness     int
	HTTPListenPort  int
	HTTPSListenPort int
	DashboardURL    string
}

type EdgeServer struct {
	Config        Config
	RestConfig    *restclient.Config
	DynamicClient dynamic.Interface
	ClientSet     *kubernetes.Clientset
	Context       context.Context
	Handler       http.Handler
}

func (s *EdgeServer) ListenAndServe(ctx context.Context) error {
	server, err := newSteveServer(ctx, s)
	if err != nil {
		return err
	}

	err = controllers.Setup(ctx, s.RestConfig, s.ClientSet, s.Config.Threadiness)
	if err != nil {
		return err
	}
	return server.ListenAndServe(ctx, s.Config.HTTPSListenPort, s.Config.HTTPListenPort, nil)
}

func New(ctx context.Context, clientConfig clientcmd.ClientConfig, cfg *Config) (*EdgeServer, error) {
	restConfig, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, err
	}
	restConfig.RateLimiter = ratelimit.None

	if err := Wait(ctx, *restConfig); err != nil {
		return nil, err
	}

	clientSet, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("kubernetes clientset create error: %s", err.Error())
	}

	dynamicClient, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("kubernetes dynamic client create error:%s", err.Error())
	}

	return &EdgeServer{
		Config:        *cfg,
		Context:       ctx,
		ClientSet:     clientSet,
		DynamicClient: dynamicClient,
		RestConfig:    restConfig,
	}, nil
}

func newSteveServer(ctx context.Context, edgeServer *EdgeServer) (*steveserver.Server, error) {
	a := auth.NewK3sAuthenticator(edgeServer.RestConfig.Host, edgeServer.ClientSet, ctx)
	handler := SetupLocalHandler(edgeServer, a)
	catalogApiServer := &catalogapi.Server{}
	return &steveserver.Server{
		RestConfig:     edgeServer.RestConfig,
		AuthMiddleware: auth.ToAuthMiddleware(a),
		Next:           handler,
		StartHooks: []steveserver.StartHook{
			catalogApiServer.Setup,
		},
	}, nil
}

func Wait(ctx context.Context, config rest.Config) error {
	client, err := kubernetes.NewForConfig(&config)
	if err != nil {
		return err
	}

	for {
		_, err := client.Discovery().ServerVersion()
		if err == nil {
			break
		}
		logrus.Infof("Waiting for server to become available: %v", err)
		select {
		case <-ctx.Done():
			return fmt.Errorf("startup canceled")
		case <-time.After(2 * time.Second):
		}
	}

	return nil
}
