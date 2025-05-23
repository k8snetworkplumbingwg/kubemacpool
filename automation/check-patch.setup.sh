# Prepare environment for kubemacpool end to end testing. This includes
# temporary Go paths and binaries.
#
# source automation/check-patch.e2e.setup.sh
# cd ${TMP_PROJECT_PATH}

tmp_dir=/tmp/kubemacpool/

rm -rf $tmp_dir
mkdir -p $tmp_dir

export TMP_PROJECT_PATH=$tmp_dir/kubemacpool
export E2E_LOGS=${TMP_PROJECT_PATH}/tests/_out
export ARTIFACTS=${ARTIFACTS-$TMP_PROJECT_PATH/artifacts}
mkdir -p $ARTIFACTS

rsync -rt --links --filter=':- .gitignore' $(pwd)/ $TMP_PROJECT_PATH
