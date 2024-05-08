# lnproxy-relay

## Running a relay

This program uses the lnd REST API to handle lightning things so you'll need an lnd.conf with,
for example:

	restlisten=localhost:8080

To configure the relay follow the usage instructions:

	usage: ./lnproxy [flags]
		-cltv-delta-alpha uint
				cltv delta alpha (default 42)
		-cltv-delta-beta uint
				cltv delta beta (default 42)
		-listen string
				interface and port over which to expose api (default "localhost:4747")
		-lnd string
				host for lnd's REST api (default "https://127.0.0.1:8080")
		-lnd-cert string
				path to lnd's self-signed cert (set to empty string for no-rest-tls=true) or base64 encoded cert or hex encoded cert (default ".lnd/tls.cert")
		-lnd-macaroon string
				a path to an lnproxy macaroon or a base64 encoded macaroon or a hex encoded macaroon
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
						uri:/chainrpc.ChainKit/GetBestBlock (default ".lnd/data/chain/bitcoin/mainnet/invoice.macaroon")
		-max-amount-msat uint
				maximum amount in msat to relay (default 1000000000)
		-max-cltv-expiry uint
				maximum cltv expiry (default 1800)
		-min-amount-msat uint
				minimum amount in msat to relay (default 10000)
		-min-cltv-expiry uint
				minimum cltv expiry (default 200)
		-payment-time-preference float
				payment time preference (default 0.9)
		-payment-timeout uint
				payment timeout (default 60)
		-routing-budget-beta uint
				routing budget beta in msat (default 1500000)
		-routing-fee-base-msat uint
				routing fee base in msat (default 1000)
		-routing-fee-ppm uint
				routing fee ppm (default 1000)

All the flags take precendence over corresponding environment variables, for example `-cltv-delta-alpha` can be set with `CTLV_DELTA_ALPHA`.

Run the binary:

	$ ./lnproxy-http-relay-openbsd-amd64-00000000 -lnd-macaroon lnproxy.macaroon
	1970/01/01 00:00:00 HTTP server listening on: localhost:4747

and on a separate terminal, test with:

	curl -s --header "Content-Type: application/json" \
		--request POST \
		--data '{"invoice":"<bolt11 invoice>"}' \
		http://localhost:4747/spec

## Expose your relay over tor

If you know how to run a server you can put your relay behind a reverse proxy and and expose it to the internet.
A simpler route is to use tor.

Install tor, then edit `/etc/tor/torrc` to add:

	HiddenServiceDir /var/tor/lnproxy/
	HiddenServicePort 80 127.0.0.1:4747

and run:

	cat /var/tor/lnproxy.org/hostname

to get the onion url and try:

	torify curl -s --header "Content-Type: application/json" \
		--request POST \
		--data '{"invoice":"<bolt11 invoice>"}' \
		http://<your .onion url>/spec

Once you're happy with it, make a PR to add your url to: https://github.com/lnproxy/lnproxy-webui2/blob/main/assets/relays.json

## Operating your relay

Sending `SIGINT` (with Ctrl-C) to the running relay will cause it to shutdown the http server
and stop accepting new invoices, it will wait for the last open invoice to expire, before fully shutting itself down.
A second `SIGINT` will cancel all open invoices and cause the relay to shutdown immediately.

When upgrading to the latest binaries, simply send one `SIGINT`
and allow the program to shut itself down gracefully.
It is safe to start the new binary immediately since the http server
from the first binary will already have shut itself down.
This way your relay can continue to proxy payments even while upgrading.

### Recovering from errors

If an unexpected error occurs when a payment to an original invoice is settled
but the accepted proxy invoice payment is not yet settled,
funds will be at risk.
This lnproxy relay tries, wherever possible, to completely shutdown in this situation:
if a single circuit does not complete as expected, the executable will
shutdown and stop accepting new invoices or sending out new payments to settle
active invoices.
This ensures that at most `MaxAmountMsat` Bitcoin will be in a "limbo" state
at any one time (the default value is 1,000,000 satoshis).

Even if such an error occurs, and an lnproxy relay circuit ends up in a limbo state,
it will almost certainly be possible to recover from the error manually.
If you notice that your relay excutable has terminated
(it's easy to set up an alert from this on *NIX systems by just adding
another command to follow the lnproxy relay command in whatever script invokes it),
you will have `CltvDeltaAlpha` blocks (by default about one day) to
manually settle the proxy payment.
To do this, simply use `lncli listinvoices` to find any invoices in the `ACCEPTED` state,
and then lookup their associated payments using the payment hash (`r_hash`).
If the payment was completed you should have a preimage you can use to
settle the `ACCEPTED` invoice.  If the payment failed, no funds are at risk,
you can cancel the hodl invoice.
