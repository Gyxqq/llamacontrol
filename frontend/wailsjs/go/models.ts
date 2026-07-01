export namespace main {
	
	export class HuggingFaceModel {
	    id: string;
	    downloads: number;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new HuggingFaceModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.downloads = source["downloads"];
	        this.description = source["description"];
	    }
	}

}

