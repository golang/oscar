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
