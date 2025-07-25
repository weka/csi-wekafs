import re
import logging
from typing import Dict, List, Annotated, Optional

import dagger
from dagger import dag, function, object_type, Ignore

from containers.builders import build_go
from apps.metricsserver import publish_metricsserver, publish_metricsserver_helm_chart, install_helm_chart

CSI_EXCLUDE_LIST = [
    "node_modules",
    ".aider*",
    "**/.git",
    ".dagger",
    "bin",
    "build",
    "terraform",
    "*.tgz",
    "tests",
]

logger = logging.getLogger(__name__)
logger.setLevel(logging.INFO)
logger.addHandler(logging.StreamHandler())

@object_type
class CsiWekafs:
    @function
    async def build_metricsserver(self,
                                 csi: Annotated[dagger.Directory, Ignore(CSI_EXCLUDE_LIST)],
                                 sock: dagger.Socket,
                                 gh_token: Optional[dagger.Secret] = None,
                                 ) -> dagger.Container:
        """Build metrics server Go application"""
        from apps.metricsserver import metricsserver_ubi
        return await metricsserver_ubi(csi, sock, gh_token)

    @function
    async def publish_metricsserver(self,
                                   csi: Annotated[dagger.Directory, Ignore(CSI_EXCLUDE_LIST)],
                                   sock: dagger.Socket,
                                   repository: str = "images.scalar.dev.weka.io:5002/csi-metricsserver",
                                   version: str = "",
                                   gh_token: Optional[dagger.Secret] = None,
                                   ) -> str:
        """Publish metrics server container to registry"""
        from apps.metricsserver import publish_metricsserver
        return await publish_metricsserver(csi, sock, repository, version, gh_token)

    @function
    async def build_helm_chart(self,
                              csi: Annotated[dagger.Directory, Ignore(CSI_EXCLUDE_LIST)],
                              sock: dagger.Socket,
                              version: str = "",
                              gh_token: Optional[dagger.Secret] = None,
                              ) -> dagger.Directory:
        """Build Helm chart for metrics server"""
        from containers.builders import helm_builder_container
        from apps.metricsserver import _calc_metricsserver_version
        
        version = await _calc_metricsserver_version(csi, version)
        
        return await (
            (await helm_builder_container(sock, gh_token))
            .with_directory("/src", csi)
            .with_workdir("/src")
            .with_exec(["sh", "-ec", f"""
        helm package charts/csi-metricsserver --version {version} --destination charts/
            """])
            .directory("/src/charts")
        )

    @function
    async def publish_helm_chart(self,
                                csi: Annotated[dagger.Directory, Ignore(CSI_EXCLUDE_LIST)],
                                sock: dagger.Socket,
                                repository: str = "images.scalar.dev.weka.io:5002/helm",
                                version: str = "",
                                gh_token: Optional[dagger.Secret] = None,
                                ) -> str:
        """Publish Helm chart to registry"""
        from apps.metricsserver import publish_metricsserver_helm_chart
        return await publish_metricsserver_helm_chart(csi, sock, repository, version, gh_token)

    @function
    async def build_scalar(self,
                           csi: Annotated[dagger.Directory, Ignore(CSI_EXCLUDE_LIST)],
                           sock: dagger.Socket,
                           repository: str = "images.scalar.dev.weka.io:5002/csi-metricsserver",
                           helm_repository: str = "images.scalar.dev.weka.io:5002/helm",
                           version: Optional[str] = None,
                           gh_token: Optional[dagger.Secret] = None,
                           registry_secret: Optional[dagger.Secret] = None, # file in format of fr3hx7l7h3p9/anton@weka.io:AUTH_TOKEN_FROM https://cloud.oracle.com/identity/domains/my-profile/auth-tokens?region=eu-frankfurt-1
                           ) -> str:
        """Build and publish metrics server to Scalar registry"""
        from apps.metricsserver import publish_metricsserver, publish_metricsserver_helm_chart
        _ = await publish_metricsserver(csi, sock,
                                       repository=repository,
                                       version=version or "",
                                       gh_token=gh_token,
                                       )
        metricsserver_helm = await publish_metricsserver_helm_chart(csi, sock,
                                                                   repository=helm_repository,
                                                                   version=version or "",
                                                                   gh_token=gh_token,
                                                                   registry_secret=registry_secret,
                                                                   )
        return metricsserver_helm

    @function
    async def deploy_scalar(self,
                            csi: Annotated[dagger.Directory, Ignore(CSI_EXCLUDE_LIST)],
                            sock: dagger.Socket,
                            kubeconfig: dagger.Secret,
                            metricsserver_values: Optional[dagger.File]=None,
                            repository: str = "images.scalar.dev.weka.io:5002/csi-metricsserver",
                            helm_repository: str = "images.scalar.dev.weka.io:5002/helm",
                            version: Optional[str] = None,
                            gh_token: Optional[dagger.Secret] = None,
                            cachebuster: Optional[str] = None,
                            ) -> str:
        """Deploy metrics server using Helm charts"""
        from apps.metricsserver import install_helm_chart
        metricsserver_helm = await self.build_scalar(csi, sock, repository, helm_repository, version, gh_token)
        install = await install_helm_chart(
            image=metricsserver_helm,
            kubeconfig=kubeconfig,
            metricsserver_repo=repository,
            values_file=metricsserver_values,
            cachebuster=cachebuster
        )
        return install


    @function
    async def publish_quay(self,
                            csi: Annotated[dagger.Directory, Ignore(CSI_EXCLUDE_LIST)],
                            sock: dagger.Socket,
                            registry_secret: dagger.Secret, # file in format of fr3hx7l7h3p9/anton@weka.io:AUTH_TOKEN_FROM https://cloud.oracle.com/identity/domains/my-profile/auth-tokens?region=eu-frankfurt-1
                            repository: str = "quay.io/weka.io/csi-metricsserver",
                            helm_repository: str = "quay.io/weka.io/helm",
                            version: Optional[str] = None,
                            gh_token: Optional[dagger.Secret] = None,
                            ) -> str:
        """Deploy metrics server using Helm charts"""
        from apps.metricsserver import install_helm_chart
        metricsserver_helm = await self.build_scalar(csi, sock, repository, helm_repository, version, gh_token, registry_secret=registry_secret)
        ret = f"published {metricsserver_helm} to {repository}"
        print(ret)
        return ret

    @function
    async def metricsserver_explore(self,
                                   csi: Annotated[dagger.Directory, Ignore(CSI_EXCLUDE_LIST)],
                                   ) -> dagger.Container:
        """Container for exploring CSI metrics server codebase"""
        return await (
            dag.container()
            .from_("ubuntu:24.04")
            .with_directory("/csi", csi)
        )