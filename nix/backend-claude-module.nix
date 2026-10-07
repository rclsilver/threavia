{
  config,
  lib,
  pkgs,
  ...
}:

let
  cfg = config.services.threavia-backend-claude;
in
{
  options.services.threavia-backend-claude = {
    enable = lib.mkEnableOption "the Threavia backend for Claude Code";

    package = lib.mkOption {
      type = lib.types.package;
      description = "The backend to run.";
    };

    instanceName = lib.mkOption {
      type = lib.types.str;
      default = config.networking.hostName;
      defaultText = lib.literalExpression "config.networking.hostName";
      description = ''
        How this machine names itself to Core. It is what a person picks from
        when starting a Session, so it should read like the machine.
      '';
    };

    coreAddress = lib.mkOption {
      type = lib.types.str;
      example = "threavia.example.com:9090";
      description = "host:port of the Core control stream (gRPC).";
    };

    coreApi = lib.mkOption {
      type = lib.types.str;
      example = "https://threavia.example.com";
      description = ''
        Base URL of the Core client API. The backend calls it once, to register;
        everything after that goes over the control stream.
      '';
    };

    user = lib.mkOption {
      type = lib.types.str;
      example = "thomas";
      description = ''
        The account the agent works as. It is not a formality: this backend
        edits that account's checkouts, runs commands with its permissions, and
        reads the Claude Code credentials in its home. On a laptop it is the
        person using the machine; the execution policy is a guard rail inside
        that account, never a sandbox around it.
      '';
    };

    stateDir = lib.mkOption {
      type = lib.types.path;
      default = "/var/lib/threavia-backend-claude";
      description = ''
        Where the backend keeps its identity, its event log and its skill cache.
        Losing it means registering again, and anything it had not yet delivered
        to Core is lost with it.
      '';
    };

    discoveryRoots = lib.mkOption {
      type = lib.types.listOf lib.types.path;
      default = [ ];
      example = lib.literalExpression ''[ "/home/thomas/git" ]'';
      description = ''
        Where to look for a project directory that has no binding on this
        machine yet. It bounds discovery and search only: what the agent may
        reach is the account's real permissions.
      '';
    };

    defaultWorkingDirectory = lib.mkOption {
      type = lib.types.path;
      description = ''
        Where a Job runs when its Session names no directory. Without one the
        agent would inherit whatever directory the service happens to start in,
        which is accidental rather than chosen.
      '';
    };

    claudePackage = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = null;
      example = lib.literalExpression "pkgs.claude-code";
      description = ''
        The Claude Code CLI the backend drives, added to the service PATH.

        Null by default, and deliberately: <literal>pkgs.claude-code</literal>
        exists but is unfree, so naming it here would make this module fail to
        evaluate for anyone who has not allowed unfree packages. It is also
        often not what someone wants — Claude Code authenticates against an
        account and updates itself, so the install is usually one the person
        already keeps. Set this, or set <literal>claudeBinary</literal>.
      '';
    };

    claudeBinary = lib.mkOption {
      type = lib.types.str;
      default = if cfg.claudePackage != null then lib.getExe cfg.claudePackage else "claude";
      defaultText = lib.literalExpression "lib.getExe config.services.threavia-backend-claude.claudePackage";
      description = "Path of the Claude Code executable.";
    };

    tls = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = true;
        description = ''
          Whether the control stream uses TLS. True by default because the
          stream carries everything an agent does; turn it off only for a Core
          on the same machine.
        '';
      };

      caFile = lib.mkOption {
        type = lib.types.nullOr lib.types.path;
        default = null;
        description = "A CA to trust beyond the system store, for a private one.";
      };
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/run/secrets/threavia-backend";
      description = ''
        A file of <literal>NAME=value</literal> lines read at start, for what
        does not belong in the Nix store. The one-shot registration token goes
        here as <literal>THREAVIA_BACKEND_REGISTRATION_TOKEN</literal>; it is
        needed only until the backend has registered, after which its identity
        lives in the state directory.
      '';
    };

    settings = lib.mkOption {
      type = lib.types.attrsOf lib.types.str;
      default = { };
      example = lib.literalExpression ''{ THREAVIA_BACKEND_MAX_CONCURRENT_RUNS = "2"; }'';
      description = ''
        Extra environment for the backend, for the settings this module does not
        name. They win over the ones it does.
      '';
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.claudePackage != null || cfg.claudeBinary != "claude";
        message = ''
          services.threavia-backend-claude: no Claude Code to drive. Set
          claudePackage, or claudeBinary to an absolute path: the service runs
          with a PATH of its own and will not find one on the person's.
        '';
      }
    ];

    # StateDirectory makes the default path and nothing else; a path chosen
    # elsewhere has to be made, or the first start fails on a missing database.
    systemd.tmpfiles.rules = lib.optional (
      cfg.stateDir != "/var/lib/threavia-backend-claude"
    ) "d ${cfg.stateDir} 0750 ${cfg.user} - -";

    systemd.services.threavia-backend-claude = {
      description = "Threavia backend for Claude Code";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];

      environment = {
        THREAVIA_BACKEND_INSTANCE_NAME = cfg.instanceName;
        THREAVIA_BACKEND_CORE_ADDRESS = cfg.coreAddress;
        THREAVIA_BACKEND_CORE_API = cfg.coreApi;
        THREAVIA_BACKEND_TLS_ENABLED = lib.boolToString cfg.tls.enable;
        THREAVIA_BACKEND_STATE_PATH = "${cfg.stateDir}/state.db";
        THREAVIA_BACKEND_SKILL_CACHE_PATH = "${cfg.stateDir}/skills";
        THREAVIA_BACKEND_CLAUDE_BINARY = cfg.claudeBinary;
        THREAVIA_BACKEND_DEFAULT_WORKING_DIRECTORY = toString cfg.defaultWorkingDirectory;
      }
      // lib.optionalAttrs (cfg.discoveryRoots != [ ]) {
        THREAVIA_BACKEND_DISCOVERY_ROOTS = lib.concatMapStringsSep "," toString cfg.discoveryRoots;
      }
      // lib.optionalAttrs (cfg.tls.caFile != null) {
        THREAVIA_BACKEND_TLS_CA_FILE = toString cfg.tls.caFile;
      }
      // cfg.settings;

      # Everything the agent runs it runs through this PATH, so a bare `git` in
      # a command has to resolve. It is deliberately short: the agent is working
      # in someone's checkout, not in a general-purpose shell.
      path = [
        pkgs.git
        pkgs.openssh
        pkgs.coreutils
        pkgs.bash
      ]
      ++ lib.optional (cfg.claudePackage != null) cfg.claudePackage;

      serviceConfig = {
        ExecStart = lib.getExe cfg.package;
        User = cfg.user;
        WorkingDirectory = cfg.stateDir;
        StateDirectory = lib.mkIf (
          cfg.stateDir == "/var/lib/threavia-backend-claude"
        ) "threavia-backend-claude";
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;

        # A backend that cannot reach Core keeps its queue and waits, so a
        # restart is the right answer to every exit.
        Restart = "always";
        RestartSec = "5s";

        # Hardening stops here on purpose. The agent's whole job is to read and
        # write the account's files and run its tools, so ProtectHome,
        # ReadOnlyPaths and a private namespace would break the thing being
        # asked for. What bounds it is the execution policy and the account's
        # own permissions.
        NoNewPrivileges = true;
      };
    };
  };
}
