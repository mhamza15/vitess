group "default" {
  targets = ["mysql80", "mysql84"]
}

target "mysql80" {
  dockerfile = "go/test/vitesst/Dockerfile.mysql80"
  tags       = ["vitesst:mysql80"]
  platforms  = ["linux/amd64"]
  cache-from = ["type=gha,scope=vitesst-mysql80"]
  cache-to   = ["type=gha,mode=max,scope=vitesst-mysql80"]
}

target "mysql84" {
  dockerfile = "go/test/vitesst/Dockerfile.mysql84"
  tags       = ["vitesst:mysql84"]
  platforms  = ["linux/amd64"]
  cache-from = ["type=gha,scope=vitesst-mysql84"]
  cache-to   = ["type=gha,mode=max,scope=vitesst-mysql84"]
}
