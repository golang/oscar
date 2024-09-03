// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

/*
This file implements a simple authorization scheme.

Users are associated with "roles" that grant them access to URL paths.
The roles are unrelated to IAM roles.
There are three roles, ordered with respect to the access they grant:

    admin > writer > reader

A URL path is also associated with a role: the minimum user role that
can access the path.

For example, a user with writer role can access paths with reader and writer
roles, but not a path with admin role.

If a user is missing from the mapping, it is given the reader role, on the
assumption that IAP will block all users that are unauthorized.

The role mappings are stored in Firestore, access to which is controlled
by IAM. Anyone with the Cloud Datastore User role can edit the mappings.
*/

import (
	"cmp"
	"context"
	"errors"

	"cloud.google.com/go/firestore"
)

// A role summarizes the permissions of a user.
type role string

const (
	reader = "reader"
	writer = "writer"
	admin  = "admin"
)

// roleRanks maps from a role to its rank.
// Higher-rank roles include (grant all the access of) lower ones.
var roleRanks = map[role]int{
	reader: 1,
	writer: 2,
	admin:  3,
}

// isAuthorized reports whether a user is authorized to access a URL.
// Access is based solely on the URL's path.
// The userRoles argument maps users to the roles they have.
// The pathRoles argument maps paths to the minimum roles they require.
// A user is authorized to access a path if its role's rank is higher than
// the minimum that the path requires.
//
// If a user is missing, it is assigned the lowest-rank role.
// If a path is missing, it is assigned the highest-rank role.
// This ensures that auth "fails closed": missing data results in denied access.
func isAuthorized(user, urlPath string, userRoles, pathRoles map[string]role) bool {
	userRole := cmp.Or(userRoles[user], reader)
	pathRole := cmp.Or(pathRoles[urlPath], admin)
	return includesRole(userRole, pathRole)
}

// includesRole reports whether r1 includes (is of higher rank than) r2.
func includesRole(r1, r2 role) bool {
	return roleRanks[r1] >= roleRanks[r2]
}

// readFirestoreRoles reads role maps from Firestore.
// Firestore stores role maps in two documents, "auth/users" and "auth/paths".
func readFirestoreRoles(ctx context.Context, c *firestore.Client) (userRoles, pathRoles map[string]role, err error) {
	coll := c.Collection("auth")
	err = errors.Join(
		decodeRoleMap(ctx, coll.Doc("users"), &userRoles),
		decodeRoleMap(ctx, coll.Doc("paths"), &pathRoles))
	if err != nil {
		return nil, nil, err
	}
	return userRoles, pathRoles, nil
}

func decodeRoleMap(ctx context.Context, dr *firestore.DocumentRef, mp *map[string]role) error {
	ds, err := dr.Get(ctx)
	if err != nil {
		return err
	}
	return ds.DataTo(mp)
}
