export namespace appcore {
	
	export class EventView {
	    state: string;
	    msg: string;
	    at: string;
	
	    static createFrom(source: any = {}) {
	        return new EventView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = source["state"];
	        this.msg = source["msg"];
	        this.at = source["at"];
	    }
	}
	export class ReleaseView {
	    toolId: string;
	    current: string;
	    latest: string;
	    available: boolean;
	    rolling: boolean;
	    sizeMb: string;
	    note: string;
	
	    static createFrom(source: any = {}) {
	        return new ReleaseView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.toolId = source["toolId"];
	        this.current = source["current"];
	        this.latest = source["latest"];
	        this.available = source["available"];
	        this.rolling = source["rolling"];
	        this.sizeMb = source["sizeMb"];
	        this.note = source["note"];
	    }
	}
	export class TaskView {
	    id: string;
	    name: string;
	    sourceUrl: string;
	    state: string;
	    stateText: string;
	    toolId: string;
	    toolName: string;
	    quality: string;
	    targets: string[];
	    unattended: boolean;
	    autoRecord: boolean;
	    weiboLive: boolean;
	    watchUrl: string;
	    lastMsg: string;
	
	    static createFrom(source: any = {}) {
	        return new TaskView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.sourceUrl = source["sourceUrl"];
	        this.state = source["state"];
	        this.stateText = source["stateText"];
	        this.toolId = source["toolId"];
	        this.toolName = source["toolName"];
	        this.quality = source["quality"];
	        this.targets = source["targets"];
	        this.unattended = source["unattended"];
	        this.autoRecord = source["autoRecord"];
	        this.weiboLive = source["weiboLive"];
	        this.watchUrl = source["watchUrl"];
	        this.lastMsg = source["lastMsg"];
	    }
	}
	export class ToolView {
	    id: string;
	    name: string;
	    builtin: boolean;
	    role: string;
	    roleText: string;
	    path: string;
	    hasOverride: boolean;
	    version: string;
	    capSummary: string;
	    canUpdate: boolean;
	    usedBy: string[];
	    inUse: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ToolView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.builtin = source["builtin"];
	        this.role = source["role"];
	        this.roleText = source["roleText"];
	        this.path = source["path"];
	        this.hasOverride = source["hasOverride"];
	        this.version = source["version"];
	        this.capSummary = source["capSummary"];
	        this.canUpdate = source["canUpdate"];
	        this.usedBy = source["usedBy"];
	        this.inUse = source["inUse"];
	    }
	}
	export class WeiboView {
	    status: string;
	    statusText: string;
	    checkedAt: string;
	    detail: string;
	    usable: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WeiboView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.status = source["status"];
	        this.statusText = source["statusText"];
	        this.checkedAt = source["checkedAt"];
	        this.detail = source["detail"];
	        this.usable = source["usable"];
	    }
	}

}

export namespace config {
	
	export class ProxySettings {
	    enabled: boolean;
	    type: string;
	    host: string;
	    port: number;
	    username: string;
	    password: string;
	
	    static createFrom(source: any = {}) {
	        return new ProxySettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.type = source["type"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.username = source["username"];
	        this.password = source["password"];
	    }
	}
	export class Settings {
	    proxy: ProxySettings;
	    maxConcurrent: number;
	    closeToTray: boolean;
	    preventSleep: boolean;
	    theme: string;
	    recordDir: string;
	    probeIntervalSec: number;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.proxy = this.convertValues(source["proxy"], ProxySettings);
	        this.maxConcurrent = source["maxConcurrent"];
	        this.closeToTray = source["closeToTray"];
	        this.preventSleep = source["preventSleep"];
	        this.theme = source["theme"];
	        this.recordDir = source["recordDir"];
	        this.probeIntervalSec = source["probeIntervalSec"];
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
	export class Target {
	    proto: string;
	    url: string;
	    key: string;
	    hasKey?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Target(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.proto = source["proto"];
	        this.url = source["url"];
	        this.key = source["key"];
	        this.hasKey = source["hasKey"];
	    }
	}
	export class Task {
	    id: string;
	    name: string;
	    sourceUrl: string;
	    toolId: string;
	    quality: string;
	    targets: Target[];
	    unattended: boolean;
	    autoRecord: boolean;
	    recordToolId: string;
	    customArgs: string;
	    weiboLive: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Task(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.sourceUrl = source["sourceUrl"];
	        this.toolId = source["toolId"];
	        this.quality = source["quality"];
	        this.targets = this.convertValues(source["targets"], Target);
	        this.unattended = source["unattended"];
	        this.autoRecord = source["autoRecord"];
	        this.recordToolId = source["recordToolId"];
	        this.customArgs = source["customArgs"];
	        this.weiboLive = source["weiboLive"];
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
	export class Tool {
	    id: string;
	    name: string;
	    builtin: boolean;
	    path: string;
	    pathOverride: string;
	    version: string;
	    capSummary: string;
	    role: string;
	    argTemplate: string[];
	    probeTemplate: string[];
	
	    static createFrom(source: any = {}) {
	        return new Tool(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.builtin = source["builtin"];
	        this.path = source["path"];
	        this.pathOverride = source["pathOverride"];
	        this.version = source["version"];
	        this.capSummary = source["capSummary"];
	        this.role = source["role"];
	        this.argTemplate = source["argTemplate"];
	        this.probeTemplate = source["probeTemplate"];
	    }
	}

}

export namespace main {
	
	export class EnvInfo {
	    version: string;
	    mode: string;
	    dataDir: string;
	
	    static createFrom(source: any = {}) {
	        return new EnvInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.mode = source["mode"];
	        this.dataDir = source["dataDir"];
	    }
	}

}

export namespace tools {
	
	export class Info {
	    version: string;
	    caps: string[];
	    flags: number;
	    summary: string;
	
	    static createFrom(source: any = {}) {
	        return new Info(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.caps = source["caps"];
	        this.flags = source["flags"];
	        this.summary = source["summary"];
	    }
	}

}

