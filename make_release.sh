#!/bin/bash

# The script is intended to release a new version of CSI plugin
# to be used internally by Weka RND

# The following envvars must be set when running the script
# DOCKER_REGISTRY_NAME quay.io/weka.io
DOCKER_IMAGE_NAME="${DOCKER_IMAGE_NAME:-csi-wekafs}"
GIT_REPO_NAME="git@github.com:weka/csi-wekafs.git"
HELM_REPO_URL="https://weka.github.io/csi-wekafs/"

export GRAY="\033[1;30m"
export LIGHT_GRAY="\033[0;37m"
export CYAN="\033[0;36m"
export LIGHT_CYAN="\033[1;36m"
export PURPLE="\033[1;35m"
export YELLOW="\033[1;33m"
export LIGHT_RED="\033[1;31m"
export NO_COLOUR="\033[0m"


ts() {
  local LINE
  while read -r LINE; do
    echo -e "$(date "+$*") $LINE"
  done
}

LOG_FILE=/dev/null

log_message() {
  # just add timestamp and redirect logs to stderr
  local LEVEL COLOR
  [[ ${1^^} =~ TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|CRITICAL|FATAL ]] && LEVEL="${1^^}" && shift || LEVEL="INFO"

  case $LEVEL in
  DEBUG) COLOR="$LIGHT_GRAY" ;;
  INFO) COLOR="$CYAN" ;;
  NOTICE) COLOR="$PURPLE" ;;
  WARNING | WARN) COLOR="$YELLOW" ;;
  ERROR | CRITICAL) COLOR="$LIGHT_RED" ;;
  esac

  ts "$(echo -e "$NO_COLOUR")[%Y-%m-%d %H:%M:%S] $(echo -e "${COLOR}${LEVEL^^}$NO_COLOUR")"$'\t' <<<"$*" | tee -a "$LOG_FILE" >&2
}

log_fatal() {
  log_message CRITICAL "$@"
  exit 1
}

git_get_latest_tag() {
  git describe --tags --abbrev=0
}

git_check_repo_clean() {
  return "$(git status --porcelain | wc -l)"
}

git_get_commits_after_latest_tag() {
  git rev-list "$(git_get_latest_tag)..HEAD" | wc -l
}

git_calc_next_tag() {
  git_get_latest_tag | awk -F. -v OFS=. 'NF==1{print ++$NF}; NF>1{if(length($NF+1)>length($NF))$(NF-1)++; $NF=sprintf("%0*d", length($NF), ($NF+1)%(10^length($NF))); print}'
}

_helm_update_charts() {
  log_message NOTICE "Updating Helm charts with correct version strings"
  local HELM_CHART_VERSION="${VERSION_STRING/-*/}"
  sed -i ./deploy/helm/csi-wekafsplugin/Chart.yaml \
      -e "s|^version: .*$|version: \"${HELM_CHART_VERSION}\"|1" \
      -e "s|^appVersion: .*$|appVersion: \"${VERSION_STRING}\"|1" \
      -e "s|\(https://github.com/weka/csi-wekafs/tree/\).*\(/deploy/helm/csi-wekafsplugin\)|\1${VERSION_STRING}\2|1" || \
        log_fatal "Could not update Helm Chart"

  sed -i ./deploy/helm/csi-wekafsplugin/values.yaml \
      -e "s|\(\&csiDriverVersion\).*|\1 \"${VERSION_STRING}\"|1" || \
        log_fatal "Could not update Helm values.yaml"

  sed -i ./deploy/kubernetes-latest/wekafs/csi-wekafs-plugin.yaml \
      -e "s|quay.io/weka.io/csi-wekafs:.*|quay.io/weka.io/csi-wekafs:${VERSION_STRING}|g" || \
        log_fatal "Could not patch csi-wekafs-plugin.yaml"
}

_helm_prepare_package() {
  helm package deploy/helm/csi-wekafsplugin || \
    log_fatal Could not package Helm chart
}

_helm_update_registry() {
  set -e
	TEMP_DIR="$(mktemp -d)"
	git clone "${GIT_REPO_NAME}" -q -b "gh-pages" "$TEMP_DIR"
  touch "$TEMP_DIR/index.yaml"
	mv "csi-wekafsplugin-${VERSION_STRING}.tgz" "$TEMP_DIR"
	cur_dir="$(pwd)"
	cd "$TEMP_DIR"
	helm repo index "$TEMP_DIR" --url "${HELM_REPO_URL}"
	git add .
	git commit -m "Added version ${VERSION_STRING}"
	git push
	cd "$cur_dir"
	rm -rf "$TEMP_DIR"
	log_message INFO "New Helm Chart version pushed successfully to repository, index updated"
  set +e
}

helm_publish() {
  _helm_update_charts
  if [[ $DEV_BUILD ]] ; then
    log_message WARNING "DEV build: not updating Helm registry"
    return
  fi
  _helm_prepare_package
  _helm_update_registry
}

_docker_login() {
  log_message INFO "Logging in to Docker registry ${DOCKER_REGISTRY_NAME}"
  docker login -u "${DOCKER_USERNAME}" -p "${DOCKER_PASSWORD}" "${DOCKER_REGISTRY_NAME}" || \
    log_fatal Could not log in into Docker repository
}

_docker_tag_image() {
  log_message NOTICE "Tagging Docker image ${DOCKER_IMAGE_NAME}:v${VERSION_STRING} -> ${DOCKER_REGISTRY_NAME}/${DOCKER_IMAGE_NAME}:v${VERSION_STRING}"
  docker tag "${DOCKER_IMAGE_NAME}:v${VERSION_STRING}" "${DOCKER_REGISTRY_NAME}/${DOCKER_IMAGE_NAME}:v${VERSION_STRING}" || \
    log_fatal "Could not tag Docker image"
}

docker_push_image() {
  _docker_tag_image
  _docker_login
  log_message NOTICE "Pushing Docker image ${DOCKER_REGISTRY_NAME}/${DOCKER_IMAGE_NAME}:v${VERSION_STRING}"
  docker push "${DOCKER_REGISTRY_NAME}/${DOCKER_IMAGE_NAME}:v${VERSION_STRING}" || \
    log_fatal "Could not push Docker image to registry"
}

build() {
  log_message INFO "Building binaries"
  docker build --build-arg VERSION="v${VERSION_STRING}" --no-cache -t "${DOCKER_IMAGE_NAME}:v${VERSION_STRING}" \
  -f Dockerfile --label revision="v${VERSION_STRING}" . || \
    log_fatal "Failed to build image"
}

_git_commit_deploy_versions() {
  set -e
  git add deploy || log_fatal "Failed to add changes to Git"
  git config --global user.email "do-not-reply@weka.io"
  git config --global user.name "CSI Deployment Bot"
  git commit -m "Release Update application version $VERSION" || log_fatal "Failed to commit changes"
  if ! git_check_repo_clean; then
    log_message ERROR "Repository not clean after committing the changes!"
    git log HEAD~1 | cat
    git status
    log_message ERROR "Resetting latest commit"
    git reset --soft HEAD~1
  fi
  set +e
}

_git_add_tag() {
  git tag "v${VERSION_STRING}"
}

_git_push() {
  log_message NOTICE "Pushing updated deployment charts to Git repository on branch $GIT_BRANCH_NAME"
  git push origin HEAD:"$GIT_BRANCH_NAME" || log_fatal "Failed to push changes to branch $GIT_BRANCH_NAME, please check!"
  git push --tags || log_fatal "Failed to push Git tag, please check!"
}

git_create_release() {
  _git_commit_deploy_versions
  if [[ -z $NO_PUBLISH ]]; then
    _git_add_tag
  else
    log_message INFO "Not adding GIT tag for DEV release Helm and not making a git release tag"
  fi
  _git_push
}

check_settings() {
  log_message NOTICE Checking for settings and dependencies
  ! which helm >/dev/null && log_fatal "Helm not installed!"
  [[ -z ${VERSION_STRING} ]] && log_fatal "Missing VERSION_STRING envvar"
  [[ -z ${DOCKER_REGISTRY_NAME} ]] && log_fatal "Missing DOCKER_REGISTRY_NAME envvar"
  [[ -z ${DOCKER_USERNAME}  ]] && log_fatal "Missing DOCKER_USERNAME envvar"
  [[ -z ${DOCKER_PASSWORD}  ]] && log_fatal "Missing DOCKER_PASSWORD envvar"

  if ! git_check_repo_clean ; then
    if [[ $GIT_BRANCH_NAME == master ]]; then
      log_fatal "Performing release on master with dirty repo is not allowed!"
    fi
    [[ -z ${ALLOW_DIRTY} ]] && log_fatal "Cannot proceed, repository is dirty!"
    [[ -z ${DEV_BUILD} ]] && log_fatal "Cannot create non-DEV release with dirty repository!"
    log_message WARNING "Allowing Dirty repository"
  fi

  VERSION_STRING="${VERSION_STRING/#v/}"
}

test() {
  pushd tests/csi-sanity || log_fatal Could not enter tests directory
  bash test.sh || log_fatal Failed tests, cannot proceed!
  popd
}

usage() {
  cat <<-DELIM

echo "$0 [--version <VERSION_STRING>] [--dev-build] [--allow-dirty] "

Optional parameters:
--------------------
--version VERSION_STRING  Package a specific release, which must be specified in X.Y.Z format
                          If not specified, the latest git tag will be incremented by 1:
                          e.g. if previous version was 0.6.6, automatically 0.6.7 will be created

--dev-build:              To be used for local development and further testing on local or remote Kubernetes.
                          In this mode:
                          - Version will be added a suffix in format of '-dev<NUM_OF_COMMITS_AFTER_LATEST_VERSION>'
                            e.g., If latest released version was 1.0.0, and 4 commits were done after this version,
                            the version will become 1.0.1-dev4
                          - Docker image will be pushed to repository, so it could be installed on remote server
                          - HOWEVER, Helm charts and deployment scripts will be modified only locally,
                            and not published on official csi-wekafs registry

--allow-dirty             Allow build when git repository is not clean and has uncommitted changes.
                          In this case, ersion will be added an additional suffix '-dirty' on top of dev version suffix

--no-publish              Do not publish release and do not make git release tag
--skip-tests              Do not peform CSI sanity tests on build

Notes and limitations:
----------------------
--allow-dirty can be used only in conjunction with --dev-build
--pushing dev builds on master branch is forbidden
--building with dirty repo forbidden on master branch
DELIM
}

main() {
  echo CSI Deployment script, copyright Weka 2021
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --allow-dirty)
        ALLOW_DIRTY=1
        shift
        ;;
      --dev-build)
        DEV_BUILD=1
        log_message NOTICE "Deploying a DEV build, which will not be published"
        shift
        ;;
      --version)
        VERSION_STRING="$2"
        shift 2
        ;;
      --help)
        usage
        exit
        ;;
      --no-publish)
        NO_PUBLISH=1
        shift
        ;;
      --skip-tests)
        NO_TESTS=1
        shift
        ;;
      *)
        usage
        log_fatal "Invalid argument '$1'"
        ;;
    esac
  done
  VERSION_STRING="${VERSION_STRING:-$(git_calc_next_tag)}"
  GIT_BRANCH_NAME=${BUILDKITE_BRANCH:-$(git branch --show-current)}

  [[ $DEV_BUILD == 1 ]] && VERSION_STRING+="-dev$(git_get_commits_after_latest_tag)"
  check_settings
  git_check_repo_clean || VERSION_STRING+="-dirty"
  log_message INFO "Deploying version ${VERSION_STRING}"
  export VERSION_STRING
  [[ -z $NO_TESTS ]] && test
  build
  docker_push_image
  helm_publish # always executed to make sure that latest version tag is updated in local chats
  git_create_release
  log_message NOTICE "All done!"
}

main "$@"