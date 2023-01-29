#!/bin/bash

# The script is intended to release a new version of CSI plugin
# to be used internally by Weka RND

# The following envvars must be set when running the script
# DOCKER_REGISTRY_NAME quay.io/weka.io
DOCKER_IMAGE_NAME="${DOCKER_IMAGE_NAME:-csi-wekafs}"
GIT_REPO_NAME="git@github.com:weka/csi-wekafs.git"
HELM_REPO_URL="https://weka.github.io/csi-wekafs/"
HELM_S3_BUCKET=csi-wekafs-plugin-helm
HELM_S3_URL="https://$HELM_S3_BUCKET.s3.eu-west-1.amazonaws.com"

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

LOG_FILE=${LOG_FILE:-/dev/null}

log_message() {
  # just add timestamp and redirect logs to stderr
  local LEVEL COLOR
  [[ ${1} =~ TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|CRITICAL|FATAL ]] && LEVEL="${1}" && shift || LEVEL="INFO"

  case $LEVEL in
  DEBUG) COLOR="$LIGHT_GRAY" ;;
  INFO) COLOR="$CYAN" ;;
  NOTICE) COLOR="$PURPLE" ;;
  WARNING | WARN) COLOR="$YELLOW" ;;
  ERROR | CRITICAL) COLOR="$LIGHT_RED" ;;
  esac

  ts "$(echo -e "$NO_COLOUR")[%Y-%m-%d %H:%M:%S] $(echo -e "${COLOR}${LEVEL}$NO_COLOUR")"$'\t' <<<"$*" | tee -a "$LOG_FILE" >&2
}

log_fatal() {
  log_message CRITICAL "$@"
  exit 1
}

# gets the latest tag from repo on current branch
git_get_latest_tag() {
  git describe --tags --abbrev=0
}

# checks if repository is dirty
git_check_repo_clean() {
  return "$(git status --porcelain | wc -l)"
}

# number of commits after latest tag
git_get_commits_after_latest_tag() {
  git rev-list "$(git_get_latest_tag)..HEAD" | wc -l | xargs
}

# calculates next version by adding 1 to the rightmost version part
calc_next_version() {
  awk -F. -v OFS=. 'NF==1{print ++$NF}; NF>1{if(length($NF+1)>length($NF))$(NF-1)++; $NF=sprintf("%0*d", length($NF), ($NF+1)%(10^length($NF))); print}'
}

# calculates next git tag
# if previous version was 0.6.6 ->  0.6.6.X where X is number of commits after tag
# if previous version was already patch 0.6.6.X -> 0.6.6.Y where Y is number of commits since 0.6.6 (and not 0.6.6.X)
git_calc_next_tag() {
  local prev_tag commits
  if (( $(awk -F"." '{print NF}' <<< "$(git_get_latest_tag)") <= 3 )); then
    # version in format of v0.6.6
    if (( $(git_get_commits_after_latest_tag) > 0 )); then
      echo "$(git_get_latest_tag).$(git_get_commits_after_latest_tag)"
    else
      git_get_latest_tag
    fi
  else
    prev_tag="$(cut -d. -f1,2,3 <<< "$(git_get_latest_tag)")"
    commits="$(git rev-list "$prev_tag..HEAD" | wc -l)"
    # version in format of v0.6.6.112
    echo "$prev_tag.$commits"
  fi
}

# losslessly converts appVersion to semver2.0 version
calc_helm_chart_version() {
  local nt="$VERSION_STRING"
  p1="$(cut -d. -f1-3 <<< "$nt" | sed 's/^v//')"
  p2="$(cut -d. -f4 <<< "$nt")"
  [[ $p2 ]] && echo "$p1-$p2" || echo "$p1"
}

# updates all required helm parameters in a specified folder (or in default folder)
helm_update_charts() {
  local CHART_DIR="${1:-"./deploy/helm"}/csi-wekafsplugin"
  log_message NOTICE "Updating Helm charts with correct version strings and generating documentation"
  yq w -i "$CHART_DIR/Chart.yaml" version "$(calc_helm_chart_version)"
  yq w -i "$CHART_DIR/Chart.yaml" appVersion "v${VERSION_STRING}"
  yq w -i "$CHART_DIR/Chart.yaml" "sources[0]" "https://github.com/weka/csi-wekafs/tree/v${VERSION_STRING}/deploy/helm/csi-wekafsplugin"
  yq w -i "$CHART_DIR/values.yaml" csiDriverVersion --anchorName csiDriverVersion "${VERSION_STRING}"
  helm-docs -s file -c $CHART_DIR
}

helm_prepare_package() {
  local TMPDIR=$(mktemp -d)
  local CURDIR="$(pwd)"
  cp -rf deploy/helm/csi-wekafsplugin "$TMPDIR"
  helm_update_charts "$TMPDIR"
  helm package "$TMPDIR/csi-wekafsplugin" || \
    log_fatal Could not package Helm chart
}

helm_upload_package_to_s3() {
  local chart_version
  chart_version="$(calc_helm_chart_version)"
  log_message DEBUG "Copying Helm release package  to S3"
  aws s3 cp "csi-wekafsplugin-${chart_version}.tgz" "s3://$HELM_S3_BUCKET/" || \
    log_fatal Failed to upload Helm chart package to s3
  log_message NOTICE "To install this release, execute:"
  echo "'helm install csi-wekafs -n csi-wekafs --create-namespace $HELM_S3_URL/csi-wekafsplugin-${chart_version}.tgz'"
}

helm_update_registry() {
  set -e
	TEMP_DIR="$(mktemp -d)"
	git clone "${GIT_REPO_NAME}" -q -b "gh-pages" "$TEMP_DIR"
  touch "$TEMP_DIR/index.yaml"
	mv "csi-wekafsplugin-$(calc_helm_chart_version).tgz" "$TEMP_DIR"
	cur_dir="$(pwd)"
	cd "$TEMP_DIR"
	helm repo index "$TEMP_DIR" --url "${HELM_REPO_URL}"
	git add .
	git commit -m "Added version ${VERSION_STRING}"
	git push
	cd "$cur_dir"
	bash

	rm -rf "$TEMP_DIR"
	log_message INFO "New Helm Chart version pushed successfully to repository, index updated"
  set +e
}

_docker_login() {
  log_message INFO "Logging in to Docker registry ${DOCKER_REGISTRY_NAME}"
  docker login -u "${DOCKER_USERNAME}" -p "${DOCKER_PASSWORD}" "${DOCKER_REGISTRY_NAME}" || \
    log_fatal Could not log in into Docker repository
}

docker_tag_image() {
  log_message NOTICE "Tagging Docker image ${DOCKER_IMAGE_NAME}:v${VERSION_STRING} -> ${DOCKER_REGISTRY_NAME}/${DOCKER_IMAGE_NAME}:v${VERSION_STRING}"
  docker tag "${DOCKER_IMAGE_NAME}:v${VERSION_STRING}" "${DOCKER_REGISTRY_NAME}/${DOCKER_IMAGE_NAME}:v${VERSION_STRING}" || \
    log_fatal "Could not tag Docker image"
}

docker_push_image() {
  _docker_login
  log_message NOTICE "Pushing Docker image ${DOCKER_REGISTRY_NAME}/${DOCKER_IMAGE_NAME}:v${VERSION_STRING}"
  docker push "${DOCKER_REGISTRY_NAME}/${DOCKER_IMAGE_NAME}:v${VERSION_STRING}" || \
    log_fatal "Could not push Docker image to registry"
}

build() {
  log_message INFO "Building binaries"
  docker build --pull --build-arg VERSION="v${VERSION_STRING}" -t "${DOCKER_IMAGE_NAME}:v${VERSION_STRING}" \
  -f Dockerfile --label revision="v${VERSION_STRING}" . || \
    log_fatal "Failed to build image"
}

check_settings() {
  log_message NOTICE Checking for settings and dependencies
  ! which helm >/dev/null && log_fatal "Helm not installed!"
  ! which yq >/dev/null && log_fatal "yq not installed!"
  ! which helm-docs >/dev/null && log_fatal "helm-docs not installed!"
  [[ -z ${VERSION_STRING} ]] && log_fatal "Missing VERSION_STRING envvar or --version flag"
  [[ -z ${DOCKER_REGISTRY_NAME} ]] && log_fatal "Missing DOCKER_REGISTRY_NAME envvar"
  [[ -z ${DOCKER_USERNAME}  ]] && log_fatal "Missing DOCKER_USERNAME envvar"
  [[ -z ${DOCKER_PASSWORD}  ]] && log_fatal "Missing DOCKER_PASSWORD envvar"

  if ! git_check_repo_clean ; then
    [[ $GIT_BRANCH_NAME == master ]] && log_fatal "Performing release on master with dirty repo is not allowed!"
    [[ $BUILD_MODE =~ beta ]] || [[ $BUILD_MODE == release ]] && log_fatal "Cannot perform release with dirty repository"
    [[ -z ${ALLOW_DIRTY} ]] && log_fatal "Cannot proceed, repository is dirty!"
    log_message WARNING "Allowing Dirty repository"
  fi

  if [[ $NO_TESTS ]]; then
    [[ $BUILD_MODE =~ beta ]] || [[ $BUILD_MODE == release ]] && log_fatal "Release without tests is not allowed"
  fi

  [[ $BUILD_MODE == local ]] && log_message WARNING "Deploying a LOCAL build only"
  [[ $BUILD_MODE == dev ]] && log_message WARNING "Deploying a DEV build, which will not be officially published"
  [[ $BUILD_MODE =~ beta ]] && log_message NOTICE "Deploying a BETA build, which will not be officially published"
  [[ $BUILD_MODE == release ]] && log_message NOTICE "Performing an official release!"

  VERSION_STRING="${VERSION_STRING/#v/}"
}

handle_testing() {
  if [[ $NO_TESTS ]]; then
    log_message WARNING "Skipping tests!"
    return
  fi
  log_message NOTICE "Testing"
  pushd tests/csi-sanity || log_fatal Could not enter tests directory
  bash test.sh || log_fatal Failed tests, cannot proceed!
  popd
  log_message NOTICE "Testing completed successfully"
}

usage() {
  cat <<-DELIM
Usage:
$0 <local|dev|beta|release> [--version <VERSION_STRING>] [--allow-dirty] [--skip-tests]

Only one of those parameters:
---------------------
local                     To be used for local development and further testing on local computer
                          - Version will be added a suffix in format of '.<NUM_OF_COMMITS_AFTER_LATEST_VERSION>-dev'
                            e.g., If latest released version was 1.0.0, and 4 commits were done after this version,
                            the version will become 1.0.0.4-dev
                          - Docker image will be built, but only locally
                          - Helm chart archive will be built locally

dev                       Also triggered automatically if BUILDKITE_BRANCH='dev'
                          To be used for further testing on Kubernetes. In this mode, on top of local
                          - Docker image will be pushed to repository, so it could be installed on remote server

beta*                     To be used for releasing a Beta version for a customer. In this mode, on top of dev:
                          - A '-beta*' suffix will be added to version string
                          - A Helm chart will is pushed S3 repository

release                   Also triggered automatically if BUILDKITE_BRANCH='ga'
                          If version is specified
                          To be used for releasing an official version of the plugin. In this mode:
                          - Docker image pushed to repository
                          - Helm chart is pushed to S3
                          - Helm chart version is published in official repository
                          - Helm charts and deployment manifests are updated inside Git repository source code
                          - Git tag of the version is created
                          - Git release is created

Optional parameters:
--------------------
--version VERSION_STRING  Can be also set via VERSION_STRING envvar.
                          Package a specific release, which must be specified in X.Y.Z format
                          If not specified, the latest git tag will be added a patch number (commits after tag):
                          e.g. if previous version was 0.6.6, automatically 0.6.6.X will be created where X is
                          the number of commits after tag v0.6.6

--allow-dirty             Allow build when git repository is not clean and has uncommitted changes.
                          In this case, version will be added an additional suffix '-dirty' on top of dev version suffix
                          Not allowed in beta and official releases

--skip-tests              Do not peform CSI sanity tests on build. Not allowed in --beta, --release
DELIM
}

handle_envvars() {
  GIT_BRANCH_NAME=${BUILDKITE_BRANCH:-$(git branch --show-current)}
  case "$GIT_BRANCH_NAME" in
    dev) BUILD_MODE=dev ;;
    ga)  BUILD_MODE=release ;;
  esac
  VERSION_STRING="${VERSION_STRING:-$(git_calc_next_tag)}"
  if [[ -z $EXPLICIT_VERSION ]]; then
    [[ $BUILD_MODE == dev ]] && VERSION_STRING+="-dev"
    [[ $BUILD_MODE =~ beta ]] && VERSION_STRING+="-$BUILD_MODE"
    git_check_repo_clean || VERSION_STRING+="-dirty"
  fi

  export VERSION_STRING
}

git_push_tag() {
  local tag="$1"
  if git rev-parse "$tag" &> /dev/null; then
    log_fatal "Could not add release tag, as tag $tag already exists"
  fi
  git tag "$tag" || log_fatal "Could not create tag $tag"
  git push --tags || log_fatal "Could not create push tag $tag"
}

git_commit_manifests() {
  git add "deploy/helm/csi-wekafsplugin/Chart.yaml" "deploy/helm/csi-wekafsplugin/values.yaml" \
    "deploy/helm/csi-wekafsplugin/README.md" "README.md"
  git commit -m "Release $VERSION_STRING"
  if ! git_check_repo_clean; then
    git reset --soft HEAD~1
    log_fatal "Could not create release commit, unexpected changes occurred in repository"
  fi
  log_message NOTICE "New commit was created for version $VERSION_STRING"
}

main() {
  echo CSI Deployment script, copyright Weka 2021
  while [[ $# -gt 0 ]]; do
    case "$1" in
      local)
        BUILD_MODE=local
        shift
        ;;
      dev)
        BUILD_MODE=dev
        shift
        ;;
      beta*)
        BUILD_MODE="$1"
        shift
        ;;
      release)
        BUILD_MODE=release
        shift
        ;;
      --version)
        VERSION_STRING="$2"
        shift 2
        ;;
      --allow-dirty)
        ALLOW_DIRTY=1
        shift
        ;;
      --skip-tests)
        NO_TESTS=1
        shift
        ;;
      --help)
        usage
        exit
        ;;
      *)
        usage
        log_fatal "Invalid argument '$1'"
        ;;
    esac
  done
  handle_envvars
  [[ -z $BUILD_MODE ]] && log_fatal "Cannot proceed, build mode not specified"
  check_settings
  log_message INFO "Deploying version ${VERSION_STRING}"
  handle_testing
  build
  docker_tag_image
  log_message INFO "Updating Helm package to version ${VERSION_STRING}"
  helm_prepare_package  # create a Helm package in termporary dir, update versions and package
  helm_update_charts  # to update the Helm chart inside repo
  helm-docs -c deploy/helm -o ../../../README.md -t ../../README.md.gotmpl -s file
  [[ $BUILD_MODE == local ]] && log_message NOTICE "Done building locally $VERSION_STRING" && exit 0
  docker_push_image
  [[ $BUILD_MODE == dev ]] && log_message NOTICE "Done building dev build $VERSION_STRING" && exit 0
  git_commit_manifests
  helm_upload_package_to_s3
  git_push_tag v"$VERSION_STRING"
  [[ $BUILD_MODE =~ beta ]] && log_message NOTICE "Done building Beta build $VERSION_STRING" && exit 0
  helm_update_registry
  log_message NOTICE "All done!"
}

main "$@"
