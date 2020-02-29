# Go File-Downloader
A little download client for when you want to download a whole directory from a fileserver.  
This tool reads the html and loads recursivly all deeper links within. This works best with simple file listing pages. The client never goes up or leaves the domain.

## How to run
Just compile and start it. It will ask for links to download.

The optional start parameters are:  
`-name name` for basic http auth  
`-pw 123456` for basic http auth  
`-link http://ubuntu.mirror.tudos.de/ubuntu/` to directly put in a link of a file or directory  
`-proxy http://10.0.0.1:1234` if you have to go through a proxy.

## Apache Downloader?
The first version did only support apache directory listing pages. This is no longer true. Any directory listing should work now. I did test nginx, apache, and lighttpd.