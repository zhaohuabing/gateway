The tetrate-ci branch contains the make files and github workflows for building and testing FIPS envoy gateway binary and image.

The `tetrate-release-fips.yaml` github workflow is triggered when a new tag `v*.*.*` is pushed to this repository. 
It builds the FIPS envoy gateway binary and image and pushes the image to Cloudsmith.

FIPS EG release is built using the corresponding envoy gateway release tag. 

* Pull the envoy gateway release tag from the open-source envoy gateway repository, e.g. `v1.2.0`.
* Create a new branch from the tag, with a suffix `-tetrate`, e.g. `v1.2.0-tetrate`.
* Copy the GitHub Actions workflows and make files from the `tetrate-ci` branch to the new branch.
* Push the new branch to this repository.
* Disable the upstream `release.yaml` GitHub workflow in the new branch.
* Tag the branch with the tetrate EG release tag, with a suffix `-tetrate`, e.g. `v1.2.0-tetrate`.
* The `tetrate-release-fips.yaml` github workflow is triggered and builds the FIPS envoy gateway image. The image is pushed to Cloudsmith repo "fips-containers.teg.tetratelabs.com/gateway".
