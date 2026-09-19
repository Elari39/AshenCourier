# GeoIP 国家库放这里

把 MaxMind DB 格式的国家库（`*.mmdb`）放进本目录，然后在 `.env` 里把
`GEOIP_DB_PATH` 指向**容器内**的路径，例如：

```
GEOIP_DB_PATH=/geoip/dbip-country-lite.mmdb
```

`*.mmdb` 已被 `.gitignore` 忽略 —— 它是外部数据（体积数 MB、每月更新一次），
不是项目源码。数据来源、下载方式与署名要求见仓库根目录 README 的「GeoIP 国家维度」。

不配置这一项完全没问题：点击明细的 `country` 字段会一律为空，跳转、计数与其余统计照旧。
