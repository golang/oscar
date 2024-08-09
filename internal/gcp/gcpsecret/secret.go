// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package gcpsecret implements a [secret.DB]
// using Google Cloud Storage's Secret Manager service.
package gcpsecret

import (
	"context"
	"encoding/hex"
	"fmt"

	secretmanager "cloud.google.com/go/secretmanager/apiv1"
	smpb "cloud.google.com/go/secretmanager/apiv1/secretmanagerpb"
	_ "golang.org/x/oscar/internal/secret"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// SecretDB implements [secret.DB] using the SecretManager in a GCP project.
// The secret names passed to [SecretDB.Get] and [SecretDB.Set] are hex-encoded
// before being passed to SecretManager, and the values are used directly
// as the Data field of a SecretPayload.
type SecretDB struct {
	client    *secretmanager.Client
	projectID string
}

// NewSecretDB returns a [SecretDB] using the GCP SecretManager of the given project.
func NewSecretDB(ctx context.Context, projectID string) (*SecretDB, error) {
	client, err := secretmanager.NewClient(ctx)
	if err != nil {
		// unreachable unless the GCP SecretManager service is in a bad state
		return nil, err
	}
	return &SecretDB{client: client, projectID: projectID}, nil
}

// Close closes the SecretDB.
func (db *SecretDB) Close() {
	if err := db.client.Close(); err != nil {
		// unreachable unless the GCP SecretManager service is in a bad state
		panic(err)
	}
}

// Get implements [secrets.DB.Get].
func (db *SecretDB) Get(name string) (secret string, ok bool) {
	ctx := context.TODO()
	hexName := hex.EncodeToString([]byte(name))
	result, err := db.client.AccessSecretVersion(ctx, &smpb.AccessSecretVersionRequest{
		Name: fmt.Sprintf("projects/%s/secrets/%s/versions/latest", db.projectID, hexName),
	})
	if err != nil {
		return "", false
	}
	return string(result.Payload.Data), true
}

// Set implements [secrets.DB.Set].
func (db *SecretDB) Set(name, secret string) {
	if err := db.set(context.TODO(), name, secret); err != nil {
		// unreachable unless the GCP SecretManager service is in a bad state
		panic(err)
	}
}

func (db *SecretDB) set(ctx context.Context, name, secret string) error {
	hexName := hex.EncodeToString([]byte(name))

	add := func() error {
		_, err := db.client.AddSecretVersion(ctx, &smpb.AddSecretVersionRequest{
			Parent:  fmt.Sprintf("projects/%s/secrets/%s", db.projectID, hexName),
			Payload: &smpb.SecretPayload{Data: []byte(secret)},
		})
		return err
	}

	err := add()
	if err == nil || !isNotFound(err) {
		return err
	}
	// Secret not found. Try to create it.
	_, err = db.client.CreateSecret(ctx, &smpb.CreateSecretRequest{
		Parent:   fmt.Sprintf("projects/%s", db.projectID),
		SecretId: hexName,
		Secret:   &smpb.Secret{Replication: &smpb.Replication{Replication: &smpb.Replication_Automatic_{}}},
	})
	if err != nil {
		return err
	}
	return add()
}

// isNotFound reports whether an error returned by the Firestore client is a NotFound
// error.
func isNotFound(err error) bool {
	return status.Code(err) == codes.NotFound
}
