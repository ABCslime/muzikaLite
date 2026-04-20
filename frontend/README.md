# MuzikaPlayer

## What it does

Frontend web application for the Muzika music player. Vue.js-based user interface for browsing, playing, and managing music queues and playlists.

## Local Setup

1. Ensure Node.js 18+ and npm 9+ are installed
2. Install dependencies: `npm install`
3. Run development server: `npm run dev`
4. Application runs on port `8080` (or configured port)

## Deployment

Deploy to Kubernetes namespace `muzika`:
```bash
kubectl apply -k k8s/
```

Image: `${ACR_NAME}.azurecr.io/muzika/muzikaplayer:latest`

Requires: Nginx-based container image, ConfigMap (if needed)
