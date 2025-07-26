{
  description = "Daedalean build tool";

  inputs.nixpkgs.url = "nixpkgs/nixos-25.05";

  outputs =
    { self, nixpkgs }:
    let
      supportedSystems = [
        "x86_64-linux"
        "x86_64-darwin"
        "aarch64-linux"
        "aarch64-darwin"
      ];
      forAllSystems = nixpkgs.lib.genAttrs supportedSystems;
      nixpkgsFor = forAllSystems (system: import nixpkgs { inherit system; });
    in
    {

      # Provide some binary packages for selected system types.
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgsFor.${system};
          dbtApp = pkgs.buildGoModule rec {
            pname = "dbt-app";
            version = "v3.2.1";
            src = ./.;
            vendorHash = "sha256-rxyqZlzEVNcnWYMWmVeGOOoW5403zy6kclhNC1H5lJo=";
            tags = [
              "semver-override=${version}"
            ];

            buildInputs = [
              pkgs.bash
              pkgs.ninja
              pkgs.git
              pkgs.go
            ];
          };
        in
        {
          dbt = dbtApp;
          dbtSandbox = pkgs.buildFHSEnv {
            name = "dbt";
            targetPkgs = pkgs: [
              dbtApp
            ];
            runScript = "dbt";
          };
        }
      );

      # Add dependencies that are only needed for development
      devShells = forAllSystems (
        system:
        let
          pkgs = nixpkgsFor.${system};
        in
        {
          default = pkgs.mkShell {
            buildInputs = with pkgs; [
              go
              gopls
              gotools
              go-tools
            ];
          };
        }
      );

      # The default package for 'nix build'. This makes sense if the
      # flake provides only one package or there is a clear "main"
      # package.
      defaultPackage = forAllSystems (system: self.packages.${system}.dbt);
    };
}
