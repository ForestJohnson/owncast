## setting up direct HLS streaming

```
curl -X POST -u "admin:abc123" 'https://stream.beta.sequentialread.com/api/admin/config/directhlsinputurl' --data-raw '{"value":"samba://forest-laptop"}'
```

```
{"success":true,"message":"direct hls input url set"}
```