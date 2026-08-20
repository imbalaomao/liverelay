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

