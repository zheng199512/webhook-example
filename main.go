package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"github.com/zheng199512/webhook-example/pkg"
	"k8s.io/klog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

func main() {

	var param pkg.WhSvrParam

	flag.IntVar(&param.Port, "port", 443, "webhook start port")
	flag.StringVar(&param.CertFile, "tls certfile", "/etc/webhook/certs/tls.crt", "file containing the x509 certficate for https")
	flag.StringVar(&param.KeyFile, "tls key", "/etc/webhook/certs/tls.key", "file containing the x509 certficate key file")

	klog.Info(fmt.Sprintf("port=%d,cert=%s,key=%s", param.Port, param.CertFile, param.KeyFile))

	pair, err := tls.LoadX509KeyPair(param.CertFile, param.KeyFile)
	if err != nil {
		klog.Errorf("failed to load key pair %v", err)
		return
	}

	whsvr := pkg.WebhookServer{
		Server: &http.Server{
			Addr:      fmt.Sprintf(":%v", param.Port),
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{pair}},
		},
		WhiteListRegistries: strings.Split(os.Getenv("WHITELIST_REGISTRIES"), ","),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/validate", whsvr.Handler)
	mux.HandleFunc("/mutate", whsvr.Handler)

	go func() {
		if err := whsvr.Server.ListenAndServeTLS("", ""); err != nil {
			klog.Error("failed to listen and server webhook server %v", err)
		}
	}()

	klog.Info("server started ")

	signalChan := make(chan os.Signal, 1)
	signal.Notify(signalChan, syscall.SIGINT, syscall.SIGTERM)
	<-signalChan

	klog.Info("GET os shutdown signal,shutting down webhook server gracefully")

	if err := whsvr.Server.Shutdown(context.Background()); err != nil {
		klog.Errorf("http server shutdown %v", err)

	}

}
