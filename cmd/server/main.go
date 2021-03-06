package main

import (
	"fmt"
	"log"
	"micro/responder/pkg/dapr"
	"net"
	"strings"

	grpcPrometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/sirupsen/logrus"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/infobloxopen/atlas-app-toolkit/server"

	"github.com/infobloxopen/atlas-app-toolkit/gorm/resource"
)

var (
	logLevel = viper.GetString("logging.level")

	// dapr configuration variables
	daprSubscribeTopic = viper.GetString("dapr.subscribe.topic")
	daprPubSubName     = viper.GetString("dapr.pubsub.name")
	daprAppPort        = viper.GetInt("dapr.appPort")
	daprGRPCPort       = viper.GetInt("dapr.grpcport")

	// internal configuration variables
	internalEnabled   = viper.GetBool("internal.enable")
	internalAddress   = viper.GetString("internal.address")
	internalPort      = viper.GetString("internal.port")
	internalServeHost = fmt.Sprintf(
		"%s:%s",
		internalAddress,
		internalPort,
	)

	// server configuration variables
	serverAddress   = viper.GetString("server.address")
	serverPort      = viper.GetString("server.port")
	serverServeHost = fmt.Sprintf(
		"%s:%s",
		serverAddress,
		serverPort,
	)
)

func main() {
	doneC := make(chan error)
	logger := NewLogger()
	pubsub, err := dapr.InitPubsub(
		daprSubscribeTopic,
		daprPubSubName,
		daprAppPort,
		daprGRPCPort,
		logger,
	)
	if err != nil {
		logger.Fatalf("Cannot initialize pubsub: %v", err)
	}
	if internalEnabled {
		go func() { doneC <- ServeInternal(logger) }()
	}

	go func() { doneC <- ServeExternal(logger, pubsub) }()

	if err := <-doneC; err != nil {
		logger.Fatal(err)
	}
}

func NewLogger() *logrus.Logger {
	logger := logrus.StandardLogger()
	logrus.SetFormatter(&logrus.JSONFormatter{})
	logger.SetReportCaller(true)

	// Set the log level on the default logger based on command line flag
	if level, err := logrus.ParseLevel(logLevel); err != nil {
		logger.Errorf("Invalid %q provided for log level", logLevel)
		logger.SetLevel(logrus.InfoLevel)
	} else {
		logger.SetLevel(level)
	}

	return logger
}

// ServeInternal builds and runs the server that listens on InternalAddress
func ServeInternal(logger *logrus.Logger) error {

	s, err := server.NewServer(
		// register metrics
		server.WithHandler("/metrics", promhttp.Handler()),
	)
	if err != nil {
		return err
	}
	l, err := net.Listen("tcp", internalServeHost)
	if err != nil {
		return err
	}

	logger.Debugf("serving internal http at %q", internalServeHost)
	return s.Serve(nil, l)
}

// ServeExternal builds and runs the server that listens on ServerAddress and GatewayAddress
func ServeExternal(logger *logrus.Logger, pubsub *dapr.PubSub) error {

	grpcServer, err := NewGRPCServer(logger, pubsub)
	if err != nil {
		logger.Fatalln(err)
	}
	grpcPrometheus.Register(grpcServer)

	s, err := server.NewServer(
		server.WithGrpcServer(grpcServer),
	)
	if err != nil {
		logger.Fatalln(err)
	}

	grpcL, err := net.Listen("tcp", serverServeHost)
	if err != nil {
		logger.Fatalln(err)
	}

	logger.Printf("serving gRPC at %s:%s", serverAddress, serverPort)

	return s.Serve(grpcL, nil)
}

func init() {
	pflag.Parse()

	if err := viper.BindPFlags(pflag.CommandLine); err != nil {
		log.Fatalf("cannot load configuration: %v", err)
	}

	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AddConfigPath(viper.GetString("config.source"))

	if viper.GetString("config.file") != "" {
		log.Printf(
			"Serving from configuration file: %s",
			viper.GetString("config.file"),
		)
		viper.SetConfigName(viper.GetString("config.file"))
		if err := viper.ReadInConfig(); err != nil {
			log.Fatalf("cannot load configuration: %v", err)
		}
	} else {
		log.Printf("Serving from default values, environment variables, and/or flags")
	}
	resource.RegisterApplication(viper.GetString("app.id"))
	resource.SetPlural()
}
