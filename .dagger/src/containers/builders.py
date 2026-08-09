from typing import Optional

from dagger import dag, Container, Directory, Socket, Secret


async def _go_builder_container(sock: Socket, gh_token: Optional[Secret] = None, version: str = "1.24-alpine") -> Container:
    """
    Returns a container suitable for building go applications.
    If gh_token is provided, it will be used to configure git to use the token.
    If gh_token is not provided, sock is used to configure git to use the ssh key.
    """
    cont = (
        dag.container()
        .from_(f"golang:{version}")
        .with_env_variable("GOPRIVATE", "github.com/weka")  # find a way to remove this to be less weka-specific?
    )

    if gh_token:
        cont = (
            cont
            .with_secret_variable("GH_TOKEN", gh_token)
            .with_exec(["sh", "-ec", """
apk add --no-cache git bash
git config --global url."https://x-access-token:$GH_TOKEN@github.com/".insteadOf "https://github.com/"
            """])
        )
    else:
        cont = (
            cont
            .with_exec(["sh", "-ec", """
apk add --no-cache git bash
apk --no-cache add ca-certificates git openssh-client
git config --global url."git@github.com:".insteadOf "https://github.com/"
mkdir -p -m 0700 ~/.ssh && ssh-keyscan github.com >> ~/.ssh/known_hosts
chmod 600 ~/.ssh/known_hosts
export GIT_SSH_COMMAND="ssh -v"
            """])
            .with_unix_socket("/tmp/ssh-agent.sock", sock)
            .with_env_variable("SSH_AUTH_SOCK", "/tmp/ssh-agent.sock")
        )

    cont = (
        cont
        .with_mounted_cache("/go/pkg/mod", dag.cache_volume("go-cache"))
        .with_mounted_cache("/root/.cache/go-build", dag.cache_volume("go-cache-root"))
    )
    return cont


async def helm_builder_container(sock: Socket, gh_token: Optional[Secret] = None) -> Container:
    cont = await _go_builder_container(sock, gh_token)
    return (
        cont
        .with_exec(["apk", "--no-cache", "add", "helm", "make"])
    )

async def helm_runner_container() -> Container:
    return (
        dag.container()
        .from_("alpine:latest")
        .with_exec(["apk", "--no-cache", "add", "helm", "kubectl"])
    )


async def build_go(
        src: Directory,
        sock: Socket,
        gh_token: Optional[Secret] = None,
        cache_deps: bool = True,
        program_path: str = "main.go",
        go_generate: bool = False,
) -> Container:
    """returns container suitable for building go applications"""

    cont = (
        (await _go_builder_container(sock, gh_token))
        .with_file("/src/go.mod", src.file("go.mod"))
        .with_file("/src/go.sum", src.file("go.sum"))
        .with_workdir("/src")
    )

    if cache_deps:
        cont = cont.with_exec(["go", "mod", "download"])

    if go_generate:
        cont = cont.with_exec(["go", "generate", "./..."])

    cont = (cont
            .with_directory("/src", src)
            .with_exec(["go", "build", "-o", "/out-binary", program_path])
            )
    return await cont


async def _uv_base() -> Container:
    return await (
        dag.container()
        .from_("ghcr.io/astral-sh/uv:alpine")
        .with_exec(["apk", "add", "--no-cache",
                    "python3",
                    "py3-pip",
                    "bash"]
                   )
    )