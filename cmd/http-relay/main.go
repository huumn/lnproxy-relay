package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/lnproxy/lnc"
	relay "github.com/lnproxy/lnproxy-relay"
)

var lnproxy_relay *relay.Relay

func specApiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Origin, X-Requested-With, Content-Type, Accept")

	x := relay.ProxyParameters{}
	err := json.NewDecoder(r.Body).Decode(&x)
	if err != nil {
		log.Println("error decoding request:", err)
		body, err := io.ReadAll(r.Body)
		if err != nil && err != io.EOF {
			log.Println("error reading request:", err)
		} else if len(body) > 0 {
			log.Println("request:", string(body))
		}
		json.NewEncoder(w).Encode(makeJsonError("bad request"))
		return
	}

	proxy_invoice, err := lnproxy_relay.OpenCircuit(x)
	if errors.Is(err, relay.ClientFacing) {
		log.Println("client facing error", strings.TrimSpace(err.Error()), "for", x)
		json.NewEncoder(w).Encode(makeJsonError(strings.TrimSpace(err.Error())))
		return
	} else if err != nil {
		log.Println("internal error", strings.TrimSpace(err.Error()), "for", x)
		json.NewEncoder(w).Encode(makeJsonError("internal error"))
		return
	}

	json.NewEncoder(w).Encode(struct {
		WrappedInvoice string `json:"proxy_invoice"`
	}{
		WrappedInvoice: proxy_invoice,
	})
}

type JsonError struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func makeJsonError(reason string) JsonError {
	return JsonError{
		Status: "ERROR",
		Reason: reason,
	}
}

func lookupEnvOrUint64(key string, defaultVal uint64) uint64 {
	if val, ok := os.LookupEnv(key); ok {
		res, err := strconv.ParseUint(val, 10, 64)
		if err != nil {
			return defaultVal
		}
		return res
	}
	return defaultVal
}

func lookupEnvOrFloat64(key string, defaultVal float64) float64 {
	if val, ok := os.LookupEnv(key); ok {
		res, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return defaultVal
		}
		return res
	}
	return defaultVal
}

func lookupEnvOrString(key string, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

func fileOrBase64OrHex(s string) []byte {
	if s == "" {
		return nil
	}
	if _, err := os.Stat(s); err == nil {
		contents, err := os.ReadFile(s)
		if err != nil {
			log.Fatalln("unable to read file:", err)
		}
		return contents
	}
	if b, err := hex.DecodeString(s); err == nil {
		return b
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b
	}
	log.Fatalln("unable to decode string:", s)
	return nil
}

func main() {
	listen := flag.String("listen", lookupEnvOrString("LISTEN", "localhost:4747"), "interface and port over which to expose api")
	lndHostString := flag.String("lnd", lookupEnvOrString("LND", "https://127.0.0.1:8080"), "host for lnd's REST api")
	lndCert := flag.String("lnd-cert", lookupEnvOrString("LND_CERT", ".lnd/tls.cert"), "path to lnd's self-signed cert (set to empty string for no-rest-tls=true) or base64 encoded cert or hex encoded cert")
	lndMacaroon := flag.String("lnd-macaroon", lookupEnvOrString("LND_MACAROON", ".lnd/data/chain/bitcoin/mainnet/invoice.macaroon"),
		`a path to an lnproxy macaroon or a base64 encoded macaroon or a hex encoded macaroon
Generate it with:
	lncli bakemacaroon --save_to lnproxy.macaroon \
		uri:/lnrpc.Lightning/DecodePayReq \
		uri:/lnrpc.Lightning/LookupInvoice \
		uri:/invoicesrpc.Invoices/AddHoldInvoice \
		uri:/invoicesrpc.Invoices/SubscribeSingleInvoice \
		uri:/invoicesrpc.Invoices/CancelInvoice \
		uri:/invoicesrpc.Invoices/SettleInvoice \
		uri:/routerrpc.Router/SendPaymentV2 \
		uri:/routerrpc.Router/EstimateRouteFee \
		uri:/chainrpc.ChainKit/GetBestBlock`)
	params := relay.NewRelayParameters()
	flag.Uint64Var(&params.MinAmountMsat, "min-amount-msat", lookupEnvOrUint64("MIN_AMOUNT_MSAT", params.MinAmountMsat), "minimum amount in msat to relay")
	flag.Uint64Var(&params.MaxAmountMsat, "max-amount-msat", lookupEnvOrUint64("MAX_AMOUNT_MSAT", params.MaxAmountMsat), "maximum amount in msat to relay")
	flag.Uint64Var(&params.RoutingBudgetBeta, "routing-budget-beta", lookupEnvOrUint64("ROUTING_BUDGET_BETA", params.RoutingBudgetBeta), "routing budget beta in msat")
	flag.Uint64Var(&params.RoutingFeeBaseMsat, "routing-fee-base-msat", lookupEnvOrUint64("ROUTING_FEE_BASE_MSAT", params.RoutingFeeBaseMsat), "routing fee base in msat")
	flag.Uint64Var(&params.RoutingFeePPM, "routing-fee-ppm", lookupEnvOrUint64("ROUTING_FEE_PPM", params.RoutingFeePPM), "routing fee ppm")
	flag.Uint64Var(&params.CltvDeltaAlpha, "cltv-delta-alpha", lookupEnvOrUint64("CLTV_DELTA_ALPHA", params.CltvDeltaAlpha), "cltv delta alpha")
	flag.Uint64Var(&params.CltvDeltaBeta, "cltv-delta-beta", lookupEnvOrUint64("CLTV_DELTA_BETA", params.CltvDeltaBeta), "cltv delta beta")
	flag.Uint64Var(&params.MaxCltvExpiry, "max-cltv-expiry", lookupEnvOrUint64("MAX_CLTV_EXPIRY", params.MaxCltvExpiry), "maximum cltv expiry")
	flag.Uint64Var(&params.MinCltvExpiry, "min-cltv-expiry", lookupEnvOrUint64("MIN_CLTV_EXPIRY", params.MinCltvExpiry), "minimum cltv expiry")
	flag.Uint64Var(&params.PaymentTimeout, "payment-timeout", lookupEnvOrUint64("PAYMENT_TIMEOUT", params.PaymentTimeout), "payment timeout")
	flag.Float64Var(&params.PaymentTimePreference, "payment-time-preference", lookupEnvOrFloat64("PAYMENT_TIME_PREFERENCE", params.PaymentTimePreference), "payment time preference")

	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "usage: %s [flags]\n\n\n", os.Args[0])
		flag.PrintDefaults()
		os.Exit(2)
	}

	flag.Parse()
	if flag.NFlag() == 0 {
		flag.Usage()
		os.Exit(2)
	}

	macaroonBytes := fileOrBase64OrHex(*lndMacaroon)
	if macaroonBytes == nil {
		log.Fatalln("unable to read lnproxy macaroon file")
	}
	macaroon := hex.EncodeToString(macaroonBytes)

	lndHost, err := url.Parse(*lndHostString)
	if err != nil {
		log.Fatalln("unable to parse lnd host url:", err)
	}
	// If this is not set then websocket errors:
	lndHost.Path = "/"

	var lndTlsConfig *tls.Config
	lndCertBytes := fileOrBase64OrHex(*lndCert)
	if lndCertBytes == nil {
		lndTlsConfig = &tls.Config{}
	} else {
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(lndCertBytes)
		lndTlsConfig = &tls.Config{RootCAs: caCertPool}
	}

	lndClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: lndTlsConfig,
		},
	}

	lnd := &lnc.Lnd{
		Host:      lndHost,
		Client:    lndClient,
		TlsConfig: lndTlsConfig,
		Macaroon:  macaroon,
	}

	lnproxy_relay = relay.NewRelayWithRelayParameters(lnd, params)

	http.HandleFunc("/spec", specApiHandler)
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "OK")
	})

	server := &http.Server{
		Addr:              *listen,
		ReadHeaderTimeout: 2 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	idleConnsClosed := make(chan struct{})
	go func() {
		sigint := make(chan os.Signal, 1)
		signal.Notify(sigint, os.Interrupt)
		<-sigint
		if err := server.Shutdown(context.Background()); err != nil {
			log.Println("HTTP server shutdown error:", err)
		}
		close(idleConnsClosed)
		log.Println("HTTP server shutdown")
	}()
	go func() {
		log.Println("HTTP server listening on:", server.Addr)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Println("HTTP server ListenAndServe error:", err)
		}
	}()
	<-idleConnsClosed

	signal.Reset(os.Interrupt)
	log.Println("waiting for open circuits...")
	lnproxy_relay.WaitGroup.Wait()
}
