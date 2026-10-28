"""Generate the viewDI documentation diagrams with the `diagrams` package.

Usage (from the repository root):
    make docs-diagrams
or:
    python docs/diagrams/generate.py

Requires Graphviz (`dot`) on PATH. Output PNG files are written to docs/img/.
"""

from pathlib import Path

from diagrams import Cluster, Diagram, Edge
from diagrams.generic.os import LinuxGeneral
from diagrams.generic.storage import Storage
from diagrams.onprem.client import Client, User
from diagrams.onprem.container import Docker, Lxc
from diagrams.onprem.vcs import Github
from diagrams.programming.flowchart import (
    Action,
    Database,
    Decision,
    Display,
    InternalStorage,
    MultipleDocuments,
    PredefinedProcess,
    StartEnd,
)
from diagrams.programming.language import Go

OUT_DIR = Path(__file__).resolve().parent.parent / "img"

GRAPH_ATTR = {
    "fontname": "Helvetica",
    "fontsize": "22",
    "pad": "0.4",
    "nodesep": "0.6",
    "ranksep": "0.9",
    "bgcolor": "white",
    "splines": "spline",
}
NODE_ATTR = {"fontname": "Helvetica", "fontsize": "12"}

# Edge colors, loosely based on the Everforest palette.
DATA = "#7fbbb3"
CONTROL = "#e69875"
ERROR = "#e67e80"
OK = "#a7c080"
MUTED = "#859289"


def diagram(name: str, title: str, direction: str = "TB") -> Diagram:
    return Diagram(
        title,
        filename=str(OUT_DIR / name),
        outformat="png",
        show=False,
        direction=direction,
        graph_attr=GRAPH_ATTR,
        node_attr=NODE_ATTR,
    )


def architecture() -> None:
    """Layered component architecture."""
    with diagram("architecture", "viewDI - Component Architecture"):
        user = User("Operator\n(terminal)")

        with Cluster("UI layer - Bubble Tea v2 (single goroutine, no I/O)"):
            app = Go("Root App Model\nrouter + keymap")
            theme = PredefinedProcess("Theme Engine\nsemantic tokens")
            with Cluster("Views"):
                views = [
                    Display("Admin\nDashboard"),
                    Display("Resources\nDashboard"),
                    Display("Table"),
                    Display("Interactive\nTree"),
                    Display("Detail tabs"),
                    Display("Updates"),
                ]

        with Cluster("State management"):
            store = Database("Store\nsnapshots + ring buffers")
            selectors = Action("Selectors")

        with Cluster("Data provider layer"):
            registry = PredefinedProcess("Engine Registry")
            supervisor = PredefinedProcess("Supervisor\nhealth + backoff")
            metrics = PredefinedProcess("Metrics\nAggregator")
            jobs = PredefinedProcess("Job Runner")
            updates = PredefinedProcess("Update Checker")
            pump = Action("Message Pump\n(coalescing)")

        with Cluster("Daemon clients / adapters"):
            docker_ad = Docker("Docker adapter\nmoby/moby/client")
            incus_ad = Lxc("Incus adapter\nlxc/incus/v6/client")
            fake_ad = InternalStorage("Fake adapter\n--mock / tests")
            cgroup = Storage("cgroup v2 reader")

        with Cluster("Host / external"):
            dockerd = Docker("dockerd\n/var/run/docker.sock")
            incusd = Lxc("incusd\n/var/lib/incus/unix.socket")
            sysfs = LinuxGeneral("/sys/fs/cgroup")
            upstream = Github("Upstream releases\n+ registries")

        user >> Edge(label="keys") >> app
        app >> theme
        app >> views
        selectors >> Edge(color=DATA) >> app
        store >> selectors

        app >> Edge(label="intents", color=CONTROL) >> jobs
        app >> Edge(label="subscribe", color=CONTROL) >> registry

        registry >> [docker_ad, incus_ad, fake_ad]
        supervisor >> Edge(style="dashed", color=MUTED) >> [docker_ad, incus_ad]
        metrics >> cgroup
        updates >> Edge(style="dashed", color=MUTED) >> upstream

        [docker_ad, incus_ad, fake_ad, metrics, jobs, updates] >> Edge(color=DATA) >> pump
        pump >> Edge(label="tea.Msg", color=DATA) >> store

        docker_ad >> dockerd
        incus_ad >> incusd
        cgroup >> sysfs


def concurrency() -> None:
    """Event streaming and concurrency model."""
    with diagram("concurrency", "viewDI - Concurrency & Event Streaming", direction="LR"):
        with Cluster("Sources (goroutines, ctx + timeout)"):
            d_events = Docker("Docker /events")
            d_stats = Docker("Docker stats\n(detail tab only)")
            d_logs = Docker("Docker logs\nfollow + stdcopy")
            i_events = Lxc("Incus /1.0/events\n(WebSocket)")
            i_metrics = Lxc("Incus /1.0/metrics\n+ instance state")
            cg = Storage("cgroup v2\none pass / tick")

        with Cluster("Bounded channels"):
            ev_ch = MultipleDocuments("events\n(never dropped)")
            mx_ch = MultipleDocuments("samples\n(latest wins)")
            log_ch = MultipleDocuments("log lines\n(ring buffer 10k)")

        pump = Action("Message Pump\nre-arming tea.Cmd")
        with Cluster("Bubble Tea loop"):
            update = Go("Update()\npure, no I/O")
            tick = Decision("UI tick\n1 Hz metrics\n<=10 Hz logs")
            view = Display("View()")

        [d_events, i_events] >> Edge(color=DATA) >> ev_ch
        [d_stats, i_metrics, cg] >> Edge(color=DATA) >> mx_ch
        d_logs >> Edge(color=DATA) >> log_ch
        [ev_ch, mx_ch, log_ch] >> pump >> Edge(label="tea.Msg") >> update
        update >> tick >> view


def supervisor() -> None:
    """Per-engine connection supervisor state machine."""
    with diagram("supervisor", "viewDI - Engine Supervisor State Machine", direction="LR"):
        start = StartEnd("start")
        connecting = Action("Connecting")
        connected = Action("Connected\nlive data")
        degraded = Action("Degraded\nyellow banner\nstale data dimmed")
        unavailable = Action("Unavailable\nred card + fix hint")
        backoff = Decision("backoff\n1s,2s,4s..30s\n+ jitter")

        start >> connecting
        connecting >> Edge(label="ping ok", color=OK) >> connected
        connected >> Edge(label="stream error /\nping timeout", color=ERROR) >> degraded
        degraded >> backoff
        backoff >> Edge(label="retry") >> connecting
        connecting >> Edge(label="EACCES / ENOENT", color=ERROR) >> unavailable
        unavailable >> Edge(label="re-probe 60s\nor ctrl+r", style="dashed", color=MUTED) >> connecting


def update_checker() -> None:
    """Engine and image update discovery plus notifications."""
    with diagram("updates", "viewDI - Update Checker & Notifications"):
        scheduler = Decision("Scheduler\nstartup + interval\nor manual 'check now'")

        with Cluster("Engine updates"):
            local_ver = Action("Installed version\nDocker /version\nIncus GET /1.0")
            pkg = PredefinedProcess("Package manager\n(read-only query)\napt / dnf / pacman / zypper")
            gh = Github("Upstream releases\nmoby/moby, lxc/incus\n(ETag cache)")
            channel = Decision("Channel filter\nIncus LTS vs stable")

        with Cluster("Image updates"):
            d_img = Docker("Docker: local RepoDigest\nvs /distribution/{ref}/json")
            i_img = Lxc("Incus: base image fingerprint\nvs remote alias (simplestreams)")

        cache = Database("~/.cache/viewDI/\nupdates.json")
        result = Action("Update results\n(diff vs last seen)")

        with Cluster("Notifications"):
            badge = Display("Header badge\n+ Admin alert")
            view = Display("Updates view\n(key 6)")
            toast = Display("Toast")
            desktop = Client("Desktop notification\nD-Bus (opt-in)")

        scheduler >> [local_ver, d_img, i_img]
        local_ver >> [pkg, gh]
        [pkg, gh] >> channel >> result
        [d_img, i_img] >> result
        result >> Edge(style="dashed", color=MUTED) >> cache
        result >> Edge(label="new only", color=CONTROL) >> [badge, view, toast, desktop]


def roadmap() -> None:
    """Development stages and their dependencies."""
    with diagram("roadmap", "viewDI - Development Stages", direction="LR"):
        s0 = StartEnd("Stage 0\nSkeleton, layout,\nkeys, themes, config")
        s1 = Action("Stage 1\nEngine contract, adapters,\nsupervisor, Table view")
        s2 = Action("Stage 2\nMetrics pipeline,\nResources + Admin dashboards")
        s3 = Action("Stage 3\nDetail view: logs,\ninspect, per-instance metrics")
        s4 = Action("Stage 4\nLifecycle actions,\njobs, create, import")
        s5 = Action("Stage 5\nInteractive Tree view")
        s6 = Action("Stage 6\nUpdate checker +\nnotifications")
        s7 = StartEnd("Stage 7\nHardening, packaging,\nv1.0.0")

        s0 >> s1
        s1 >> [s2, s3, s5]
        s2 >> Edge(label="metrics reuse", style="dashed", color=MUTED) >> s3
        s3 >> s4
        s2 >> Edge(label="sparklines", style="dashed", color=MUTED) >> s5
        s4 >> Edge(label="actions in tree", style="dashed", color=MUTED) >> s5
        s4 >> Edge(label="pull / recreate", style="dashed", color=MUTED) >> s6
        [s5, s6] >> s7


def testing() -> None:
    """Testing strategy: one contract suite, three engine backends, fault injection."""
    with diagram("testing", "viewDI - Testing Strategy"):
        with Cluster("Test suites"):
            unit = Action("Unit + fuzz\nparsers, rates, backoff,\nring buffers, themes")
            contract = PredefinedProcess("Engine contract suite\n(same tests, every backend)")
            golden = Display("teatest golden files\n80x24 / 120x40 / themes")
            chaos = Decision("Chaos scenarios\nhang, drop, EACCES,\nflood, malformed JSON")
            bench = Action("Benchmarks\nrender + CPU budgets")

        with Cluster("Engine backends"):
            fake = InternalStorage("Fake engine\nscripted + fault injection\n(every CI run)")
            fixtures = MultipleDocuments("Recorded fixtures\nhttptest on unix socket\n(every CI run)")
            with Cluster("Real daemons (-tags=integration, nightly)"):
                real_d = Docker("dockerd")
                real_i = Lxc("incusd")

        ui = Go("UI models\n(views, components)")

        contract >> [fake, fixtures, real_d, real_i]
        golden >> ui
        ui >> Edge(style="dashed", color=MUTED) >> fake
        chaos >> Edge(color=ERROR) >> fake
        bench >> ui
        unit >> Edge(style="invis") >> golden


def main() -> None:
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    architecture()
    concurrency()
    supervisor()
    update_checker()
    roadmap()
    testing()
    print(f"Diagrams written to {OUT_DIR}")


if __name__ == "__main__":
    main()
