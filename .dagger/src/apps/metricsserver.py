from typing import Optional

import dagger
from dagger import dag, Container, Directory, Socket, Secret

from containers.builders import build_go


async def metricsserver_ubi(src: Directory, sock: Socket, gh_token: Optional[Secret] = None) -> Container:
    """Returns container suitable for building go applications"""
    metricsserver = await build_go(src, sock, gh_token, cache_deps=True, program_path="cmd/metricsserver/main.go",
                                   go_generate=False)

    return await (
        dag.container()
        .from_(
            "registry.access.redhat.com/ubi9/ubi@sha256:9ac75c1a392429b4a087971cdf9190ec42a854a169b6835bc9e25eecaf851258")
        .with_file("/metricsserver", metricsserver.file("/out-binary"))
    )


async def _calc_metricsserver_version(src: Directory, version: str = "") -> str:
    if not version:
        digest = await src.digest()
        sha = digest.split(":")[-1]
        version = f"v999.0.0-{sha[:12]}"
    return version


async def publish_metricsserver(src: Directory, sock: Socket, repository: str, version: str = "",
                                gh_token: Optional[Secret] = None) -> str:
    """Returns container suitable for building go applications"""
    metricsserver = await metricsserver_ubi(src, sock, gh_token)
    # Compute a compact version by hashing combined digests
    version = await _calc_metricsserver_version(src, version)

    return await metricsserver.publish(f"{repository}:{version}")


async def publish_metricsserver_helm_chart(
        src: Directory,
        sock: Socket,
        repository: str,
        version: str = "",
        gh_token: Optional[Secret] = None,
        registry_secret: Optional[dagger.Secret] = None, # file in format of fr3hx7l7h3p9/anton@weka.io:AUTH_TOKEN_FROM https://cloud.oracle.com/identity/domains/my-profile/auth-tokens?region=eu-frankfurt-1
) -> str:
    from containers.builders import helm_builder_container

    version = await _calc_metricsserver_version(src, version)

    builder = await (
        (await helm_builder_container(sock, gh_token))
        .with_directory("/src", src)
        .with_workdir("/src")
    )

    if registry_secret is not None:
        builder = builder.with_mounted_secret("/registry-secret", registry_secret)

    base_repository = repository.rpartition("/")[0]
    base_repository = base_repository.rpartition("/")[0] # cutting out helm, then cutting out namespace. very bound to OCI atm and broken for others
    await (
        builder.with_exec(["sh", "-ec", f"""
    helm package charts/csi-metricsserver --version {version} --app-version {version} --destination charts/
        """])
        .with_exec(["sh", "-ec", f"""
        if [ -f /registry-secret ]; then
            AUTH=$(cat /registry-secret)
            USERNAME=$(echo $AUTH | cut -d: -f1)
            PASSWORD=$(echo $AUTH | cut -d: -f2-)
            echo "Logging in to Helm registry {repository} as $USERNAME"
            helm registry login {base_repository} -u $USERNAME -p $PASSWORD
            echo "Pushing Helm chart to {repository}"
        fi
        helm push charts/csi-metricsserver-*.tgz oci://{repository}
"""])
        .stdout()
    )
    return f"{repository}/csi-metricsserver:{version}"


async def install_helm_chart(
        image: str, kubeconfig:
        dagger.Secret,
        metricsserver_repo: str,
        values_file: Optional[dagger.File] = None,
        cachebuster: Optional[str] = None,
) -> str:
    from containers.builders import helm_runner_container
    repo, _, version = image.rpartition(":")

    # TODO: Add pre-load?
    cont = await (
        (await helm_runner_container())
    )
    if values_file is not None:
        cont = cont.with_file("/values.yaml", values_file)

    return await (cont
                  .with_mounted_secret("/kubeconfig", kubeconfig)
                  .with_env_variable("KUBECONFIG", "/kubeconfig")
                  .with_exec(["sh", "-ec", f"""
        echo {cachebuster} > /dev/null
        helm upgrade --install metricsserver oci://{repo} --namespace csi-metricsserver-system \
            --version {version} \
            --create-namespace \
            --set image.repository={metricsserver_repo} \
        {"--values /values.yaml" if values_file is not None else ""}
         """])
                  .stdout()
                  )
