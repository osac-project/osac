/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0
*/
package grpcserver

import (
	"context"

	"github.com/osac-project/osac/fulfillment-service/internal/cmd/service/start/idpclient"
	"github.com/osac-project/osac/fulfillment-service/internal/idp"
	"github.com/osac-project/osac/fulfillment-service/internal/trust"
)

func (r *runnerContext) createIDPClient(ctx context.Context, caPool *trust.CertPool) (*idp.Client, error) {
	return idpclient.New(ctx, idpclient.Config{
		Logger:           r.logger,
		BaseURL:          r.args.idpURL,
		IssuerURL:        r.args.authIssuerURL,
		ClientID:         r.args.idpClientID,
		ClientIDFile:     r.args.idpClientIDFile,
		ClientSecret:     r.args.idpClientSecret,
		ClientSecretFile: r.args.idpClientSecretFile,
		CAPool:           caPool,
	})
}
