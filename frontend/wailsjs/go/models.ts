export namespace app {
	
	export class DiskProject {
	    url: string;
	    diskUsage: number;
	
	    static createFrom(source: any = {}) {
	        return new DiskProject(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.diskUsage = source["diskUsage"];
	    }
	}
	export class DiskInfo {
	    total: number;
	    free: number;
	    projects: DiskProject[];
	
	    static createFrom(source: any = {}) {
	        return new DiskInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.free = source["free"];
	        this.projects = this.convertValues(source["projects"], DiskProject);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class GPU {
	    vendor: string;
	    count: number;
	    names: string[];
	    driver: string;
	    cuda: string;
	    vram: number;
	
	    static createFrom(source: any = {}) {
	        return new GPU(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.vendor = source["vendor"];
	        this.count = source["count"];
	        this.names = source["names"];
	        this.driver = source["driver"];
	        this.cuda = source["cuda"];
	        this.vram = source["vram"];
	    }
	}
	export class HistPoint {
	    t: time.Time;
	    running: number;
	
	    static createFrom(source: any = {}) {
	        return new HistPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.t = this.convertValues(source["t"], time.Time);
	        this.running = source["running"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class HostSpec {
	    os: string;
	    osVersion: string;
	    cpu: string;
	    cores: number;
	    flops: number;
	    memory: number;
	    diskFree: number;
	    diskTotal: number;
	    cpid: string;
	    gpus: GPU[];
	
	    static createFrom(source: any = {}) {
	        return new HostSpec(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.os = source["os"];
	        this.osVersion = source["osVersion"];
	        this.cpu = source["cpu"];
	        this.cores = source["cores"];
	        this.flops = source["flops"];
	        this.memory = source["memory"];
	        this.diskFree = source["diskFree"];
	        this.diskTotal = source["diskTotal"];
	        this.cpid = source["cpid"];
	        this.gpus = this.convertValues(source["gpus"], GPU);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class MsgLine {
	    seq: number;
	    pri: number;
	    time: number;
	    body: string;
	    project: string;
	
	    static createFrom(source: any = {}) {
	        return new MsgLine(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.seq = source["seq"];
	        this.pri = source["pri"];
	        this.time = source["time"];
	        this.body = source["body"];
	        this.project = source["project"];
	    }
	}
	export class ProjectInfo {
	    name: string;
	    url: string;
	    venue: string;
	    userName: string;
	    teamName: string;
	    userCredit: number;
	    rac: number;
	    hostCredit: number;
	    hostRac: number;
	    share: number;
	    suspended: boolean;
	    noMoreWork: boolean;
	    pending: boolean;
	    ended: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProjectInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.venue = source["venue"];
	        this.userName = source["userName"];
	        this.teamName = source["teamName"];
	        this.userCredit = source["userCredit"];
	        this.rac = source["rac"];
	        this.hostCredit = source["hostCredit"];
	        this.hostRac = source["hostRac"];
	        this.share = source["share"];
	        this.suspended = source["suspended"];
	        this.noMoreWork = source["noMoreWork"];
	        this.pending = source["pending"];
	        this.ended = source["ended"];
	    }
	}
	export class Totals {
	    running: number;
	    paused: number;
	    queued: number;
	    errors: number;
	    downloads: number;
	    uploads: number;
	    memory: number;
	    credit: number;
	    rac: number;
	
	    static createFrom(source: any = {}) {
	        return new Totals(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.running = source["running"];
	        this.paused = source["paused"];
	        this.queued = source["queued"];
	        this.errors = source["errors"];
	        this.downloads = source["downloads"];
	        this.uploads = source["uploads"];
	        this.memory = source["memory"];
	        this.credit = source["credit"];
	        this.rac = source["rac"];
	    }
	}
	export class Transfer {
	    name: string;
	    url: string;
	    projectName: string;
	    upload: boolean;
	    total: number;
	    done: number;
	    progress: number;
	    paused: boolean;
	    finished: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Transfer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.url = source["url"];
	        this.projectName = source["projectName"];
	        this.upload = source["upload"];
	        this.total = source["total"];
	        this.done = source["done"];
	        this.progress = source["progress"];
	        this.paused = source["paused"];
	        this.finished = source["finished"];
	    }
	}
	export class Task {
	    name: string;
	    wu: string;
	    url: string;
	    projectName: string;
	    status: string;
	    progress: number;
	    elapsed: number;
	    cpuTime: number;
	    eta: number;
	    deadline: number;
	    cpTime: number;
	    exit: number;
	    mem: number;
	    resources: string;
	    slot: number;
	    appVersion: string;
	    active: boolean;
	    suspended: boolean;
	    ready: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Task(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.wu = source["wu"];
	        this.url = source["url"];
	        this.projectName = source["projectName"];
	        this.status = source["status"];
	        this.progress = source["progress"];
	        this.elapsed = source["elapsed"];
	        this.cpuTime = source["cpuTime"];
	        this.eta = source["eta"];
	        this.deadline = source["deadline"];
	        this.cpTime = source["cpTime"];
	        this.exit = source["exit"];
	        this.mem = source["mem"];
	        this.resources = source["resources"];
	        this.slot = source["slot"];
	        this.appVersion = source["appVersion"];
	        this.active = source["active"];
	        this.suspended = source["suspended"];
	        this.ready = source["ready"];
	    }
	}
	export class Snapshot {
	    hostId: string;
	    demo: boolean;
	    online: boolean;
	    error: string;
	    version: string;
	    ts: time.Time;
	    hostInfo: HostSpec;
	    projects: ProjectInfo[];
	    tasks: Task[];
	    transfers: Transfer[];
	    messages: MsgLine[];
	    totals: Totals;
	    taskMode: string;
	    netMode: string;
	
	    static createFrom(source: any = {}) {
	        return new Snapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.hostId = source["hostId"];
	        this.demo = source["demo"];
	        this.online = source["online"];
	        this.error = source["error"];
	        this.version = source["version"];
	        this.ts = this.convertValues(source["ts"], time.Time);
	        this.hostInfo = this.convertValues(source["hostInfo"], HostSpec);
	        this.projects = this.convertValues(source["projects"], ProjectInfo);
	        this.tasks = this.convertValues(source["tasks"], Task);
	        this.transfers = this.convertValues(source["transfers"], Transfer);
	        this.messages = this.convertValues(source["messages"], MsgLine);
	        this.totals = this.convertValues(source["totals"], Totals);
	        this.taskMode = source["taskMode"];
	        this.netMode = source["netMode"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class StatPoint {
	    day: string;
	    hostCredit: number;
	    userCredit: number;
	
	    static createFrom(source: any = {}) {
	        return new StatPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.day = source["day"];
	        this.hostCredit = source["hostCredit"];
	        this.userCredit = source["userCredit"];
	    }
	}
	export class StatSeries {
	    url: string;
	    name: string;
	    daily: StatPoint[];
	
	    static createFrom(source: any = {}) {
	        return new StatSeries(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.url = source["url"];
	        this.name = source["name"];
	        this.daily = this.convertValues(source["daily"], StatPoint);
	    }
	}
	
	export class XferPoint {
	    when: number;
	    up: number;
	    down: number;
	
	    static createFrom(source: any = {}) {
	        return new XferPoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.when = source["when"];
	        this.up = source["up"];
	        this.down = source["down"];
	    }
	}

}

export namespace main {
	
	export class HostCfgJSON {
	    id: string;
	    name: string;
	    host: string;
	    port: number;
	    password: string;
	    demo: boolean;
	
	    static createFrom(source: any = {}) {
	        return new HostCfgJSON(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.password = source["password"];
	        this.demo = source["demo"];
	    }
	}

}

export namespace time {
	
	export class Time {
	
	
	    static createFrom(source: any = {}) {
	        return new Time(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	
	    }
	}

}