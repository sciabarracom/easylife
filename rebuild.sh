sudo dpkg --purge openserverless
sudo k3s-killall.sh
sudo k3s-uninstall.sh
sudo userdel trustant
sudo groupdel trustant
sudo rm -Rvf /home/trustant
rm -Rvf ~/.ops ~/.local/bin/ops
bash setup.sh
bash prepare.sh

