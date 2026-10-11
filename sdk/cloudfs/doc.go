// Package cloudfs is a Go SDK for China Mobile 139 cloud drive (yun.139.com).
//
// # Mount paths
//
// The SDK exposes a flat virtual tree: each configured account is mounted under
// a prefix such as /yun139, and every path passed to a method starts with that
// prefix. "/" itself is the virtual root and lists the configured mounts.
//
//	/yun139                the mount's own root directory
//	/yun139/photos/a.jpg  a.jpg inside the mount
//	/                     all configured mounts
//
// # Configuration
//
// The default config file is ~/.cloudfs.json:
//
//	{
//	  "storages": [
//	    {
//	      "mount_path": "/yun139",
//	      "driver": "139Yun",
//	      "addition": {
//	        "Authorization": "...",
//	        "Username": "13800138000",
//	        "Password": "...",
//	        "RootFolderID": "",
//	        "Type": "personal_new"
//	      }
//	    }
//	  ],
//	  "default_mount": "/yun139"
//	}
//
// "driver" is the name a driver registers under in cloudfso/op. This SDK ships
// 139Yun only (see drivers/yun139); "yun139" is accepted as an alias because
// that spelling appears throughout the README.
//
// # Example
//
//	client, err := cloudfs.NewClientFromFile(context.Background(), "")
//	if err != nil {
//	    panic(err)
//	}
//
//	files, err := client.List(ctx, "/yun139/我的文件")
//	for _, f := range files {
//	    fmt.Printf("%s\t%d\t%s\n", f.Name, f.Size, f.Type)
//	}
//
//	client.Download(ctx, "/yun139/我的文件/photo.jpg", os.Stdout)
//	client.Upload(ctx, "/yun139/我的文件", "/tmp/photo.jpg", nil)
//
//	sp, _ := client.Space(ctx, "/yun139")       // sp.Total / sp.Used / sp.Free
//	info, _ := client.UserInfo(ctx, "/yun139")  // info.Account / info.Nickname

package cloudfs
