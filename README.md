# Go Apache-Downloader
For when you want to download a whole directory from an apache fileserver.
This tool traverses the directoy tree and loads all files within into the current directoy.

The Apache server needs to support the F parameter. (e.g. http://ubuntu.mirror.tudos.de/ubuntu/?F=0)

The optional start parameters are:  
`-name name` for basic http auth  
`-pw 123456` for pasic http auth  
`-link http://ubuntu.mirror.tudos.de/ubuntu/` to directly put in a link of a file or directory  
`-proxy http://10.0.0.1:1234` if you have to go through a proxy.
