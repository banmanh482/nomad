job "service-connect-native" {
  type = "service"

  group "group" {
    service {
      name = "example"

      connect {
        native = true
      }
    }
  }
}
