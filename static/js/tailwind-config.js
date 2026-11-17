// Identidade visual do Livredo — fonte única da paleta e da tipografia,
// carregada logo depois do Tailwind pela loja, pelo painel do admin e pelo do
// vendedor.
//
// O verde-musgo é a essência da marca: a cor das encadernações clássicas de
// biblioteca, aqui um pouco mais saturada. As cores que o acompanham vêm todas
// do espectro marrom, como couro, madeira e papel envelhecido:
//   papel     — o creme das páginas, fundo de toda a loja
//   caramelo  — marrom dourado, para ornamentos, filetes e destaques
//   canela    — marrom avermelhado, acento quente (preços, selos)
//   camurca   — castanho claro, para superfícies secundárias
//   cafe      — o marrom quase preto do texto impresso
(function () {
  var musgo = {
    50: '#EFF7F2', 100: '#D6EDDF', 200: '#ADDBBF', 300: '#7FC39B', 400: '#50A574',
    500: '#2F8757', 600: '#256F47', 700: '#1F5C3C', 800: '#1A4A31', 900: '#133826', 950: '#0B2317'
  };
  var canela = {
    50: '#FAF1EA', 100: '#F2DDCC', 200: '#E6BFA2', 300: '#D39C74', 400: '#BD7B4F',
    500: '#A3613A', 600: '#8A4F2F', 700: '#6F3F27', 800: '#57321F', 900: '#3F2417'
  };

  tailwind.config = {
    theme: {
      extend: {
        colors: {
          musgo: musgo,
          canela: canela,
          papel: {
            50: '#FCF8F1', 100: '#F7EFE2', 200: '#EFE2CC', 300: '#E2CFAE', 400: '#CFB388', 500: '#B8966A'
          },
          caramelo: {
            100: '#F5E7D0', 200: '#EACFA3', 300: '#DDB577', 400: '#CC9A52', 500: '#B37F3A', 600: '#94672D', 700: '#734F23'
          },
          camurca: {
            50: '#F8F2EA', 100: '#F0E4D3', 200: '#E3CFB4', 300: '#D0B38E', 400: '#B99470', 500: '#9C7856'
          },
          cafe: {
            400: '#93826F', 500: '#75634F', 600: '#5A4A3A', 700: '#43362A', 800: '#30261D', 900: '#211A14'
          },
          // Nomes antigos, ainda usados pelas páginas de pagamento e pelos
          // painéis. Apontá-los para a paleta atual mantém tudo coerente sem
          // reescrever essas páginas: forest é o verde da marca e shell, que
          // era o acento, vira canela.
          forest: musgo,
          shell: canela
        },
        fontFamily: {
          serif: ['Fraunces', 'Georgia', 'Cambria', 'serif'],
          sans: ['"DM Sans"', 'system-ui', '-apple-system', 'Segoe UI', 'sans-serif']
        }
      }
    }
  };
})();
