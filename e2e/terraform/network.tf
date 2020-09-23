data "aws_vpc" "default" {
  default = true
}

data "aws_subnet" "default" {
  availability_zone = var.availability_zone
  vpc_id            = data.aws_vpc.default.id
}

resource "aws_security_group" "servers" {
  name   = "${local.random_name}-servers"
  vpc_id = data.aws_vpc.default.id

  # ssh
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Nomad
  ingress {
    from_port   = 4646
    to_port     = 4646
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Consul
  ingress {
    from_port   = 8500
    to_port     = 8500
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # server-to-server
  ingress {
    from_port = 0
    to_port   = 0
    protocol  = "-1"
    self      = true
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

resource "aws_security_group" "clients" {
  name   = "${local.random_name}-clients"
  vpc_id = data.aws_vpc.default.id

  # ssh
  ingress {
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Fabio
  ingress {
    from_port   = 9998
    to_port     = 9999
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # HDFS NameNode UI
  ingress {
    from_port   = 50070
    to_port     = 50070
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # HDFS DataNode UI
  ingress {
    from_port   = 50075
    to_port     = 50075
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # Spark history server UI
  ingress {
    from_port   = 18080
    to_port     = 18080
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  # client-to-client
  ingress {
    from_port = 0
    to_port   = 0
    protocol  = "-1"
    self      = true
  }

  egress {
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }
}

# open up Nomad communication from client to server
resource "aws_security_group_rule" "nomad_client_to_server" {
  type              = "ingress"
  from_port         = 4646
  to_port           = 4648
  protocol          = "tcp"
  source_security_group_id = aws_security_group.clients.id
  security_group_id = aws_security_group.servers.id
}

# open up Nomad communication from server to client without opening clients
# wide open to the world
resource "aws_security_group_rule" "nomad_server_to_client" {
  type              = "ingress"
  from_port         = 4646
  to_port           = 4648
  protocol          = "tcp"
  source_security_group_id = aws_security_group.servers.id
  security_group_id = aws_security_group.clients.id
}

# open up Consul communication from client to server
resource "aws_security_group_rule" "consul_serf_client_to_server" {
  type              = "ingress"
  from_port         = 8300
  to_port           = 8302
  protocol          = "-1"
  source_security_group_id = aws_security_group.clients.id
  security_group_id = aws_security_group.servers.id
}

# open up Consul communication from server to client without opening clients
# wide open to the world
resource "aws_security_group_rule" "consul_serf_server_to_client" {
  type              = "ingress"
  from_port         = 8300
  to_port           = 8302
  protocol          = "-1"
  source_security_group_id = aws_security_group.servers.id
  security_group_id = aws_security_group.clients.id
}

# open up Consul communication from client to server
resource "aws_security_group_rule" "consul_http_client_to_server" {
  type              = "ingress"
  from_port         = 8500
  to_port           = 8502
  protocol          = "-1"
  source_security_group_id = aws_security_group.clients.id
  security_group_id = aws_security_group.servers.id
}

# open up Consul communication from server to client without opening clients
# wide open to the world
resource "aws_security_group_rule" "consul_http_server_to_client" {
  type              = "ingress"
  from_port         = 8500
  to_port           = 8502
  protocol          = "-1"
  source_security_group_id = aws_security_group.servers.id
  security_group_id = aws_security_group.clients.id
}

resource "aws_security_group" "nfs" {
  name   = "${local.random_name}-nfs"
  vpc_id = data.aws_vpc.default.id

  ingress {
    from_port       = 2049
    to_port         = 2049
    protocol        = "tcp"
    security_groups = [aws_security_group.clients.id]
  }
}
