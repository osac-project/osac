/*
Copyright (c) 2026 Red Hat Inc.

Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file except in compliance with the
License. You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on an
"AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the specific
language governing permissions and limitations under the License.
*/

package servers

import (
	"context"
	"errors"
	"log/slog"

	"github.com/osac-project/osac/fulfillment-service/internal/auth"
	privatev1 "github.com/osac-project/osac/proto/gen/osac/private/v1"
	publicv1 "github.com/osac-project/osac/proto/gen/osac/public/v1"
)

type SelfSubjectAccessReviewsServerBuilder struct {
	logger    *slog.Logger
	evaluator auth.AuthorizationEvaluator
}

var _ publicv1.SelfSubjectAccessReviewsServer = (*SelfSubjectAccessReviewsServer)(nil)

type SelfSubjectAccessReviewsServer struct {
	publicv1.UnimplementedSelfSubjectAccessReviewsServer

	logger    *slog.Logger
	evaluator auth.AuthorizationEvaluator
	inMapper  *GenericMapper[*publicv1.SelfSubjectAccessReview, *privatev1.SelfSubjectAccessReview]
	outMapper *GenericMapper[*privatev1.SelfSubjectAccessReview, *publicv1.SelfSubjectAccessReview]
	private   *PrivateSelfSubjectAccessReviewsServer
}

// NewSelfSubjectAccessReviewsServer creates a new builder for the self subject access reviews server.
func NewSelfSubjectAccessReviewsServer() *SelfSubjectAccessReviewsServerBuilder {
	return &SelfSubjectAccessReviewsServerBuilder{}
}

// SetLogger sets the logger. This is mandatory.
func (b *SelfSubjectAccessReviewsServerBuilder) SetLogger(value *slog.Logger) *SelfSubjectAccessReviewsServerBuilder {
	b.logger = value
	return b
}

// SetEvaluator sets the authorization evaluator. This is mandatory.
func (b *SelfSubjectAccessReviewsServerBuilder) SetEvaluator(value auth.AuthorizationEvaluator) *SelfSubjectAccessReviewsServerBuilder {
	b.evaluator = value
	return b
}

// Build builds the self subject access reviews server.
func (b *SelfSubjectAccessReviewsServerBuilder) Build() (result *SelfSubjectAccessReviewsServer, err error) {
	if b.logger == nil {
		return nil, errors.New("logger is mandatory")
	}
	if b.evaluator == nil {
		return nil, errors.New("evaluator is mandatory")
	}

	inMapper, err := NewGenericMapper[*publicv1.SelfSubjectAccessReview, *privatev1.SelfSubjectAccessReview]().
		SetLogger(b.logger).
		SetStrict(true).
		Build()
	if err != nil {
		return nil, err
	}
	outMapper, err := NewGenericMapper[*privatev1.SelfSubjectAccessReview, *publicv1.SelfSubjectAccessReview]().
		SetLogger(b.logger).
		SetStrict(false).
		Build()
	if err != nil {
		return nil, err
	}

	delegate, err := NewPrivateSelfSubjectAccessReviewsServer().
		SetLogger(b.logger).
		SetEvaluator(b.evaluator).
		Build()
	if err != nil {
		return nil, err
	}

	result = &SelfSubjectAccessReviewsServer{
		logger:    b.logger,
		evaluator: b.evaluator,
		inMapper:  inMapper,
		outMapper: outMapper,
		private:   delegate,
	}
	return
}

func (s *SelfSubjectAccessReviewsServer) Create(ctx context.Context, request *publicv1.SelfSubjectAccessReviewsCreateRequest) (*publicv1.SelfSubjectAccessReviewsCreateResponse, error) {
	// Map the public request to private
	privateObj := &privatev1.SelfSubjectAccessReview{}
	if request.GetObject() != nil {
		err := s.inMapper.Copy(ctx, request.GetObject(), privateObj)
		if err != nil {
			return nil, err
		}
	}

	privateReq := &privatev1.SelfSubjectAccessReviewsCreateRequest{
		Object: privateObj,
	}

	// Call the private server
	privateResp, err := s.private.Create(ctx, privateReq)
	if err != nil {
		return nil, err
	}

	// Map the private response back to public
	publicObj := &publicv1.SelfSubjectAccessReview{}
	if privateResp.GetObject() != nil {
		err := s.outMapper.Copy(ctx, privateResp.GetObject(), publicObj)
		if err != nil {
			return nil, err
		}
	}

	publicResp := &publicv1.SelfSubjectAccessReviewsCreateResponse{
		Object: publicObj,
	}

	return publicResp, nil
}
