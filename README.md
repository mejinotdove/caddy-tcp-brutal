Caddy module for tcp brutal
===========================================

This is a [Caddy](https://caddyserver.com/) module to activate tcp brutal on tcp listener.
Tcp brutal is a kernel module that provide tcp congestion control. please refer to [tcp-brutal](https://github.com/apernet/tcp-brutal) for more information.

This Caddy module works on Linux kernel 5.8 or later. 
Earlier kernel versions might work, but installing the tcp-brutal module will be more complicated. 

This module isn't compatible with Windows or macOS because they don't support kernel modules installation.

System Requirements
------------------------------------------
Installation tcp brutal:
```bash
bash <(curl -fsSL https://tcp.hy2.sh/)
```

Check tcp-brutal module activated:
```bash
lsmod | grep -i brutal 
````

Compile caddy with tcp-brutal
------------------------------------------
```bash
go install github.com/caddyserver/xcaddy@latest
xcaddy build --with github.com/maintell/caddy-tcp-brutal
```

Usage
------------------------------------------
```bash
./caddy run --config ./caddy.json
```

Check caddy tcp-brutal module activated, after the first time you access the website of caddy, you should see log output like this:

```bash
tcp_brutal: fd=8 congestion="brutal"
tcp_brutal: fd=8 TCP_BRUTAL_PARAMS set rate=25000000 cwnd_gain=20

 ```