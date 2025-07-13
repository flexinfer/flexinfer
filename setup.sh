#!/usr/bin/env bash
# setup.sh  —  Provision a fresh Ubuntu‑like host so that Codex (or CI) can
#              compile & test the flexinfer/flexinfer repository.

set -euo pipefail

######################  Tunables  ######################
GO_VERSION="${GO_VERSION:-1.24.4}"          # override: GO_VERSION=1.25 ./setup.sh
CLONE_DIR="${CLONE_DIR:-$HOME/flexinfer}"   # where the repo will live
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-flexinfer-dev}"
########################################################

echo ">> Updating apt cache & installing base packages…"
sudo apt-get update -y
sudo apt-get install -y --no-install-recommends \
    build-essential git curl ca-certificates gnupg lsb-release make jq

######### Go ###################################################################
if ! command -v go >/dev/null || [[ "$(go version | awk '{print $3}')" != "go${GO_VERSION}" ]]; then
  echo ">> Installing Go ${GO_VERSION}…"
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tgz
  sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf /tmp/go.tgz
  rm /tmp/go.tgz
fi
export PATH=$PATH:/usr/local/go/bin:${HOME}/go/bin
grep -qxF 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' ~/.bashrc || \
  echo 'export PATH=$PATH:/usr/local/go/bin:$HOME/go/bin' >> ~/.bashrc

######### Docker ###############################################################
if ! command -v docker >/dev/null; then
  echo ">> Installing Docker Engine…"
  curl -fsSL https://get.docker.com | sh
  sudo usermod -aG docker "$USER"
  echo "   (Log out/in or run 'newgrp docker' for group change to take effect)"
fi

######### kubectl ##############################################################
if ! command -v kubectl >/dev/null; then
  echo ">> Installing kubectl…"
  KREL="$(curl -Ls https://dl.k8s.io/release/stable.txt)"
  curl -LO "https://dl.k8s.io/release/${KREL}/bin/linux/amd64/kubectl"
  sudo install -m 0755 kubectl /usr/local/bin
  rm kubectl
fi

######### Helm #################################################################
if ! command -v helm >/dev/null; then
  echo ">> Installing Helm 3…"
  curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
fi

######### kind #################################################################
if ! command -v kind >/dev/null; then
  echo ">> Installing kind…"
  KIND_VER="$(curl -s https://api.github.com/repos/kubernetes-sigs/kind/releases/latest \
              | jq -r '.tag_name')"
  curl -Lo kind "https://kind.sigs.k8s.io/dl/${KIND_VER}/kind-linux-amd64"
  chmod +x kind
  sudo mv kind /usr/local/bin/
fi

######### Clone & build ########################################################
if [[ ! -d "${CLONE_DIR}" ]]; then
  git clone https://github.com/flexinfer/flexinfer.git "${CLONE_DIR}"
fi
cd "${CLONE_DIR}"

echo ">> Tidying Go modules & building binaries…"
go mod download
make build
make test

######### Optional demo cluster ################################################
if [[ "${RUN_DEMO:-yes}" == "yes" ]]; then
  echo ">> Spinning up a local kind cluster '${KIND_CLUSTER_NAME}'…"
  kind create cluster --name "${KIND_CLUSTER_NAME}" \
      --config hack/kind-mixed-gpu.yaml || true

  echo ">> Installing FlexInfer via Helm…"
  helm repo add flexinfer https://flexinfer.github.io/charts
  helm repo update
  helm install flexinfer flexinfer/flexinfer \
      --namespace flexinfer-system --create-namespace

  echo ">> Deploying sample Llama‑3 8B model…"
  kubectl apply -f examples/llama3-8b.yaml
  echo "   Use 'kubectl get pods -l flexinfer.ai/model=llama3-8b -o wide' to watch placement."
fi

echo "✅  Setup complete."
