#!/bin/bash
BANNER="WEKA CSI Volume Migration Utility. Copyright 2024 WEKA"
LOG_LEVEL=4
CSI_VOLUMES_DIR="csi-volumes"
FILESYSTEM=
LOG_FILE="/tmp/$(basename "$0")-$$.log"
PROCESSED_DIRS=0
UPDATED_DIRS=0
SKIPPED_DIRS=0
WARNINGS=0
FAILURES=0

usage() {
  cat <<-DELIM

The migration utility enables hard capacity enforcement on CSI volumes created before WEKA CSI plugin version 0.7.0. It can be run from any host within the WEKA cluster where the CSI volumes are located.

The following OS packages or utilities are required:

* xattr (from the xattr package)
* getfattr (from the attr package)
* jq (from the jq package)
* WEKA client software

For more details, refer to [Upgrade legacy persistent volumes for capacity enforcement](upgrade-legacy-pv.md).

Usage: $0 <filesystem_name> [--csi-volumes-dir <CSI_VOLUMES_DIR>] [--endpoint-address IP_ADDRESS:PORT]
       $0 --help

Optional parameters:
--------------------
--debug             Execute with debug level logging
--csi-volumes-dir   Assume CSI volumes are stored in different directory on the filesystem. Default is "csi-volumes"
--endpoint-address  API_ADDRESS:PORT of a WEKA backend server for stateless clients. Specify the port if the host is not connected to the cluster.

DELIM
}

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

log_message() {
  # just add timestamp and redirect logs to stderr
  local LEVEL COLOR
  [[ ${1} =~ TRACE|DEBUG|INFO|NOTICE|WARN|WARNING|ERROR|CRITICAL|FATAL ]] && LEVEL="${1}" && shift || LEVEL="INFO"

  case $LEVEL in
  DEBUG) COLOR="$LIGHT_GRAY"; [[ $LOG_LEVEL ]] && [[ $LOG_LEVEL -lt 5 ]] && return ;;
  INFO) COLOR="$CYAN"; [[ $LOG_LEVEL ]] && [[ $LOG_LEVEL -lt 4 ]] && return ;;
  NOTICE) COLOR="$PURPLE"; [[ $LOG_LEVEL ]] && [[ $LOG_LEVEL -lt 3 ]] && return ;;
  WARNING | WARN) COLOR="$YELLOW" ; [[ $LOG_LEVEL ]] && [[ $LOG_LEVEL -lt 2 ]] && return ;;
  ERROR | CRITICAL | FATAL) COLOR="$LIGHT_RED";;
  esac
  ts "$(echo -e "$NO_COLOUR")[%Y-%m-%d %H:%M:%S] $(echo -e "${COLOR}${LEVEL}${NO_COLOUR}")"$'\t' <<< "$*" | tee -a "$LOG_FILE" >&2
}

log_fatal() {
  log_message CRITICAL "$@"
  exit 1
}

check_settings() {
  log_message NOTICE Checking for settings and dependencies
  which getfattr &>/dev/null || log_fatal "attr package not installed!"
  which xattr &>/dev/null || log_fatal "xattr package not installed!"
  which jq &>/dev/null || log_fatal "jq package is not installed!"
  which weka &>/dev/null || log_fatal "Weka software not installed!"
  log_message "Settings OK"
}

cleanup () {
  log_message DEBUG "Initiating cleanup sequence"
  [[ $PUSHED ]] && popd &>/dev/null
  if [[ $TMPDIR ]]; then
    if [[ $FILESYSTEM ]]; then
      if umount "$TMPDIR" &>> "$LOG_FILE" ; then
        log_message DEBUG "Filesystem $FILESYSTEM successfully unmounted"
      else
        log_message ERROR "Failed to umount $FILESYSTEM from $TMPDIR"
      fi
    else
        log_message DEBUG "Nothing to unmount"
    fi
    rm -rf "$TMPDIR"
  fi
}

mount_fs() {
  local FS="$1"
  log_message NOTICE "Mounting filesystem $FILESYSTEM and accessing CSI volumes directory $CSI_VOLUMES_DIR..."
  if mount -t wekafs -o acl "${ENDPOINT_ADDRESS}${FS}" "$TMPDIR" 2>&1 | tee -a "$LOG_FILE"; then
    log_message NOTICE "Successfully mounted filesystem $FILESYSTEM"
  else
    log_fatal "Failed to mount filesystem $FILESYSTEM"
  fi
}

get_directory_quota() {
  RET="$(weka fs quota list -p "$1" --all -R -J | jq '.[0].hard_limit_bytes')"
  [[ $RET == null ]] && return 1
  echo -n "$RET"
}

set_directory_quota() {
  weka fs quota set --hard "$2"B "$1"
}

get_legacy_capacity() {
  RET="$(getfattr "$1" -n "user.weka_capacity" --only-values)" && echo -n "$RET" || return 1
}

remove_xattr_capacity() {
  setfattr "$1" -x "user.weka_capacity" &>> "$LOG_FILE"
}

migrate_directory() {
  log_message INFO "Processing directory '$1'"
  local DIR_PATH="$1"
  local XATTR_CAPACITY QUOTA_CAPACITY NEW_CAPACITY
  (( PROCESSED_DIRS ++ ))
  log_message DEBUG "Fetching legacy capacity of $DIR_PATH"
  XATTR_CAPACITY=$(get_legacy_capacity "$DIR_PATH" 2>> "$LOG_FILE")

  if [[ $XATTR_CAPACITY ]]; then
    log_message DEBUG "Current capacity: $XATTR_CAPACITY"
    NEW_CAPACITY=$XATTR_CAPACITY
    QUOTA_CAPACITY="$(get_directory_quota "$DIR_PATH")"
    if [[ $QUOTA_CAPACITY ]]; then
      log_message DEBUG "Current quota: $QUOTA_CAPACITY"
      if (( QUOTA_CAPACITY != XATTR_CAPACITY )); then
        NEW_CAPACITY=$(( XATTR_CAPACITY > QUOTA_CAPACITY ? XATTR_CAPACITY : QUOTA_CAPACITY ))
        log_message WARNING "Current quota doesn't match previously set volume capacity, setting max value $NEW_CAPACITY!"
      fi
    fi
    log_message INFO "Creating quota of $NEW_CAPACITY bytes for directory $DIR_PATH"
    if set_directory_quota "$DIR_PATH" "$NEW_CAPACITY"; then
      (( UPDATED_DIRS ++ ))
      log_message INFO "Quota was successfully set for directory $DIR_PATH"
      if remove_xattr_capacity "$DIR_PATH"; then
        log_message DEBUG "Removed legacy capacity from directory $DIR_PATH"
      else
        log_message WARNING "Failed to remove legacy capacity from directory $DIR_PATH"
        (( WARNINGS ++ ))
      fi
    else
      log_message ERROR "Failed to set quota on directory $DIR_PATH"
      (( FAILURES ++ ))
    fi
  else
    QUOTA_CAPACITY="$(get_directory_quota "$DIR_PATH")"
    if [[ $QUOTA_CAPACITY ]]; then
      log_message INFO "Directory $DIR_PATH already has quota, skipping..."
      (( SKIPPED_DIRS ++ ))
    else
      log_message ERROR "Could not obtain capacity for directory $DIR_PATH, assuming not a CSI volume"
      (( FAILURES ++ ))
    fi
  fi
}

main() {
  echo "$BANNER"
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --help)
        usage
        exit 0
        ;;
      --endpoint-address)
        ENDPOINT_ADDRESS="$2"
        shift 2
        ;;
      --debug)
        LOG_LEVEL=5
        shift
        ;;
      --csi-volumes-dir)
        CSI_VOLUMES_DIR="$2"
        shift 2
        ;;
      *)
        if [[ -z $FILESYSTEM ]]; then
          FILESYSTEM="$1"
          shift
        else
          usage
          log_fatal "Invalid argument '$1'"
        fi
        ;;
    esac
  done
  [[ $ENDPOINT_ADDRESS ]] && ENDPOINT_ADDRESS="$ENDPOINT_ADDRESS/"

  if [[ -z $FILESYSTEM ]]; then
    usage
    log_fatal "Filesystem name not specified"
  fi
  check_settings
  log_message NOTICE "Initializing volume migration for filesystem $FILESYSTEM"
  TMPDIR="$(mktemp -d)" && log_message DEBUG "Created a temporary directory $TMPDIR"
  mount_fs "$FILESYSTEM"
  if pushd "$TMPDIR/$CSI_VOLUMES_DIR" &>/dev/null ; then
    PUSHED=1
  else
    log_fatal "Could not find directory $CSI_VOLUMES_DIR on filesystem $FILESYSTEM"
  fi
  log_message NOTICE "Starting Persistent Volume migration"

  for file in *; do
    migrate_directory "$file"
  done

  log_message NOTICE "Migration process complete!"
  log_message NOTICE "$PROCESSED_DIRS directories processed"
  log_message NOTICE "$UPDATED_DIRS directories migrated successfully"
  log_message NOTICE "$SKIPPED_DIRS directories skipped"
  if (( WARNINGS > 0 )); then
    log_message WARNING "$WARNINGS warnings occurred, please inspect log"
  fi
  if (( FAILURES > 0 )); then
    log_message ERROR "$FAILURES failures occurred, please inspect log"
  fi
  log_message NOTICE "Full migration log can be found in $LOG_FILE"
}

trap cleanup EXIT
main "$@"
