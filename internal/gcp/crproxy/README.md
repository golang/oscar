# Cloud Run Proxy

An App Engine app that is a reverse proxy for a Cloud Run service.

## Deployment

Create an app.yaml file as described in crproxy.go.
Then run
```
gcloud app deploy
```

## Testing

Use [gcloud's dev_appserver.py](https://cloud.google.com/appengine/docs/standard/tools/using-local-server?tab=go)
or deploy to a separate environment.

## Auth

Authentication is done by the IAP (Identity-Aware Proxy). It should be restricted
to members who need to access the Cloud Run service.

Since IAP does not allow fine-grained access control, this app implements a simple
path-based authorization scheme described in auth.go. The ACLs for this scheme are
stored in the default Firestore database in the auth collection.
Modify the auth mappings when the set of users or URL paths changes.

When a user is added or removed, edit the "auth/users" Firestore document.
Adding a user involves adding a new field with the user's email address.
The value of the field is the user's role:

- "reader" for read-only access.
- "writer" for the ability to make changes, like setting the log level.
- "admin" for access to all paths.

The default is "reader", so users with reader access don't need to be added.

Adding a path involves editing the "auth/paths" Firestore document.
The new field's name is the path, and the value is the minimum user role
that can access the path. The default minimum is "admin". Use "reader" for
paths that only read information, "writer" for paths that change the service's
state, and "admin" for paths that make serious changes.
