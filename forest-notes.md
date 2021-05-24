## setting up direct HLS streaming

```
curl -X POST -u "admin:abc123" 'localhost:8080/api/admin/config/directhlsinputurl' --data-raw '{"value":"samba://192.168.0.40"}'
```

```
{"success":true,"message":"direct hls input url set"}
```