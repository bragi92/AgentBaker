#!/bin/bash
set -euxo pipefail

source vhdbuilder/scripts/windows/automate_helpers.sh

az login --identity

echo "Build Id is $1"

latest_image_version_2019=$(az vm image show --urn MicrosoftWindowsServer:WindowsServer:2019-Datacenter-Core-smalldisk:latest --query 'id' -o tsv | awk -F '/' '{print $NF}')
image_version=$(echo "$latest_image_version_2019" | cut -c 12-)
  
build_id=$1

set +x
github_access_token=$2
set -x

branch_name=releaseNotes/$image_version
pr_title="ReleaseNotes"

generate_release_notes() {
    echo "Build ID for the release is $build_id"
    for artifact in $(az pipelines runs artifact list --run-id $build_id | jq -r '.[].name'); do    # Retrieve what artifacts were published
        if [[ $artifact == *"vhd-release-notes"* ]]; then
            sku=$(echo $artifact | cut -d "-" -f4-) # Format of artifact is vhd-release-notes-<name of sku>
            included_skus+="$sku,"
        fi
    done
    echo "SKUs for release notes are $included_skus"
    go run vhdbuilder/release-notes/autonotes/winnote.go --build $build_id --include ${included_skus%?}
}

set_git_config
if [ `git branch --list $branch_name` ]; then
    git checkout $branch_name
    git pull origin
    git checkout master -- .
else
    create_branch $branch_name
fi

generate_release_notes
git status
set +x
create_pull_request $image_version $github_access_token $branch_name $pr_title