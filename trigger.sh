SNAP=$(date '+%y %m %d %H %M' | awk '{printf "%s%s%02d%s%02d\n", $1, substr("abcdefghijkl",$2+0,1), $3, substr("abcdefghijklmnopqrstuvwx",$4+1,1), $5}')
git tag 0.9.0+$SNAP
git push origin --tags
