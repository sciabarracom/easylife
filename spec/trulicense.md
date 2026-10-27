create the trulicence command written in go stored in trulicense folder

0. check it is running as root
if not ask to use sudo (it is normally installed setuid)
and then check you can access to k3s as ruoot, execute k3s kubectl get ns
and there is a namespace openserverless

1. it asks to type a license in the format decribed  in
![license format](./license_format.md)

2. validate the licence checking the signature

the public key is embedded as master_key_pub

and extract the hosts in the license,

3.  for each host, provision in namespace openserverless an nginx proxy that for each listed host:

- accepts http://<name>.n7s.co and do a revderse proxy to the controller with Host: http://miniops.me
- accepts http://<app>.<name>.n7s.co and do a revderse proxy to the controller with Host: http://<app>.miniops.me
- support web sockets
- accept up to 1g payload

provisioning hosts is idempotent, you can repat it
but you may add multiple license creating multiple hosts from different licenses

4. genrate a random user provisioning token 32 digits
then save it

kubectl -n openserverless create secret generic openserverless-user-provisioning-token \
  --from-file=token=provisioning-token

if there is an existing token notifies there is one and ask if replacing y/b

then shows the token and says: write down this token it won't be shown again

5. there is also --reset command that will clean the proxy and the user provisioning


